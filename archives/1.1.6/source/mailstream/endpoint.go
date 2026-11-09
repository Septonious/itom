package mailstream

import (
	"bytes"
	"context"
	"errors"
	"net"
	"time"
)

// Connect completes a fresh two-flight Noise handshake, prepares the fresh
// DATA namespace, and requires an authenticated bidirectional readiness exchange.
// Selected IMAP sockets alone never become a connected VPN state.
func Connect(ctx context.Context, link Link, identity string, psk []byte) (*Session, error) {
	if codec, ok := link.(negotiatedMailCodec); ok {
		codec.setOptimizedMailCodec(false)
	}
	h, request, err := initiate(psk, identity)
	if err != nil {
		return nil, err
	}
	var cipher *cipherPair
	var ready []byte
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			packet := request
			if cipher != nil {
				packet = ready
			}
			if err = link.Send(ctx, packet, true); err != nil && !errors.Is(err, ErrCapacity) {
				return nil, err
			}
			timer.Reset(time.Second)
		case packet, ok := <-link.Receive():
			if !ok {
				return nil, ErrClosed
			}
			kind, id, _, parseErr := parseHeader(packet)
			if parseErr != nil || id != h.id {
				continue
			}
			if kind == accept && cipher == nil {
				cipher, err = h.finish(packet)
				if err != nil {
					return nil, err
				}
				body, _ := (frame{kind: frameReady}).encode()
				ready, _, err = cipher.seal(body)
				if err != nil {
					return nil, err
				}
				if err = link.Send(ctx, ready, true); err != nil && !errors.Is(err, ErrCapacity) {
					return nil, err
				}
			}
			if kind == sealed && cipher != nil {
				body, seq, err := cipher.open(packet)
				if err != nil {
					continue
				}
				f, err := decodeFrame(body)
				if err != nil || seq != 0 || f.stream != 0 || f.offset != 0 || len(f.data) != 0 {
					continue
				}
				if f.kind == frameBusy {
					return nil, ErrBusy
				}
				if f.kind != frameReadyAck {
					continue
				}
				if confirmed, ok := link.(interface{ ConfirmPeer([]byte) }); ok {
					confirmed.ConfirmPeer(packet)
				}
				// Admission is authenticated before either endpoint's extra
				// DATA reader can consume the provider's connection allowance.
				if err = link.Attach(ctx, h.id); err != nil {
					return nil, err
				}
				// Startup's caller deadline is not the established connection's
				// lifetime. The owner closes Session/Link explicitly.
				s := newSession(context.WithoutCancel(ctx), link, cipher, false, nil)
				s.wg.Add(1)
				go func() { defer s.wg.Done(); routeSession(s, link.Receive()) }()
				s.offerOptimizedMailCodec()
				return s, nil
			}
		}
	}
}

func routeSession(s *Session, incoming <-chan []byte) {
	for {
		select {
		case <-s.ctx.Done():
			return
		case packet, ok := <-incoming:
			if !ok {
				s.fail(ErrClosed)
				return
			}
			_, id, _, err := parseHeader(packet)
			if err != nil || id != s.cipher.id {
				continue
			}
			select {
			case s.input <- packet:
			case <-s.ctx.Done():
				return
			}
		}
	}
}

type candidateSession struct {
	cipher       *cipherPair
	hello, reply []byte
	denied       []byte
	created      time.Time
}

// Serve owns the standing control receiver across user VPN restarts. A replayed
// HELLO cannot replace an established session: the new ephemeral Noise split
// key must authenticate READY before Attach selects its DATA namespace.
// The prototype serves one configured client identity per Link.
func Serve(ctx context.Context, link Link, identity string, psk []byte, dial func(context.Context, string, string) (net.Conn, error), connected func(*Session)) error {
	return ServeWithOptions(ctx, link, identity, psk, dial, ServerOptions{Connected: connected})
}

type ServerOptions struct {
	Connected func(*Session)
	// Admit reserves a physical DATA socket pair only after peer authentication.
	// Its release must not run until the previous DATA readers have joined.
	Admit       func() (release func(), err error)
	IdleTimeout time.Duration
}

func ServeWithOptions(ctx context.Context, link Link, identity string, psk []byte, dial func(context.Context, string, string) (net.Conn, error), options ServerOptions) error {
	if len(psk) != 32 || len(identity) == 0 || len(identity) > 64 {
		return ErrProtocol
	}
	var active *Session
	var current *candidateSession
	var release func()
	var activeDone <-chan struct{}
	closeActive := func() {
		if active == nil {
			if codec, ok := link.(negotiatedMailCodec); ok {
				codec.setOptimizedMailCodec(false)
			}
			return
		}
		active.Close()
		if codec, ok := link.(negotiatedMailCodec); ok {
			codec.setOptimizedMailCodec(false)
		}
		if carrier, ok := link.(interface{ Detach(SessionID) }); ok {
			carrier.Detach(active.cipher.id)
		}
		active, activeDone, current = nil, nil, nil
		if release != nil {
			release()
			release = nil
		}
	}
	pending := map[SessionID]*candidateSession{}
	defer closeActive()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-activeDone:
			closeActive()
		case <-ticker.C:
			for old, candidate := range pending {
				if time.Since(candidate.created) > 30*time.Second {
					delete(pending, old)
					if tracker, ok := link.(interface{ trackHandshake(SessionID, bool) }); ok {
						tracker.trackHandshake(old, false)
					}
				}
			}
			if active != nil && options.IdleTimeout > 0 && time.Since(time.Unix(0, active.lastInbound.Load())) > options.IdleTimeout {
				closeActive()
			}
		case packet, ok := <-link.Receive():
			if !ok {
				return ErrClosed
			}
			kind, id, _, err := parseHeader(packet)
			if err != nil {
				continue
			}
			if kind == hello {
				c := pending[id]
				if active != nil && active.cipher.id == id {
					c = current
				}
				if c != nil {
					if bytes.Equal(c.hello, packet) {
						_ = link.Send(ctx, c.reply, true)
					}
					continue
				}
				for old, candidate := range pending {
					if time.Since(candidate.created) > 30*time.Second {
						delete(pending, old)
						if tracker, ok := link.(interface{ trackHandshake(SessionID, bool) }); ok {
							tracker.trackHandshake(old, false)
						}
					}
				}
				if len(pending) >= 16 {
					continue
				}
				cipher, reply, err := respond(psk, identity, packet)
				if err != nil {
					continue
				}
				if tracker, ok := link.(interface{ trackHandshake(SessionID, bool) }); ok {
					tracker.trackHandshake(id, true)
				}
				pending[id] = &candidateSession{cipher: cipher, hello: bytes.Clone(packet), reply: reply, created: time.Now()}
				if err = link.Send(ctx, reply, true); err != nil && !errors.Is(err, ErrCapacity) {
					return err
				}
				continue
			}
			if kind != sealed {
				continue
			}
			if active != nil && active.cipher.id == id {
				body, _, err := active.cipher.open(packet)
				if err == nil {
					f, err := decodeFrame(body)
					if err == nil && f.kind == frameReady && f.stream == 0 && f.offset == 0 && len(f.data) == 0 {
						// READY_ACK must repeat the original ciphertext/nonce. A
						// retransmitted handshake does not create a new session.
						if current != nil {
							_ = link.Send(ctx, current.reply, true)
						}
						continue
					}
				}
				select {
				case active.input <- packet:
				case <-active.ctx.Done():
				case <-ctx.Done():
					return ctx.Err()
				}
				continue
			}
			candidate := pending[id]
			if candidate == nil {
				continue
			}
			body, seq, err := candidate.cipher.open(packet)
			if err != nil {
				continue
			}
			f, err := decodeFrame(body)
			if err != nil || seq != 0 || f.kind != frameReady || f.stream != 0 || f.offset != 0 || len(f.data) != 0 {
				continue
			}
			if candidate.denied != nil {
				_ = link.Send(ctx, candidate.denied, true)
				continue
			}
			var nextRelease func()
			if options.Admit != nil {
				nextRelease, err = options.Admit()
				if err != nil {
					if !errors.Is(err, ErrBusy) {
						return err
					}
					plain, _ := (frame{kind: frameBusy}).encode()
					candidate.denied, _, err = candidate.cipher.seal(plain)
					if err != nil {
						return err
					}
					_ = link.Send(ctx, candidate.denied, true)
					continue
				}
			}
			closeActive()
			if err = link.Attach(ctx, id); err != nil {
				if carrier, ok := link.(interface{ Detach(SessionID) }); ok {
					carrier.Detach(id)
				}
				if nextRelease != nil {
					nextRelease()
				}
				delete(pending, id)
				if tracker, ok := link.(interface{ trackHandshake(SessionID, bool) }); ok {
					tracker.trackHandshake(id, false)
				}
				continue
			}
			release = nextRelease
			plain, _ := (frame{kind: frameReadyAck}).encode()
			reply, _, err := candidate.cipher.seal(plain)
			if err != nil {
				return err
			}
			// Reserve nonce zero for READY_ACK before any session worker can seal.
			active = newSession(ctx, link, candidate.cipher, true, dial)
			activeDone = active.Done()
			current = &candidateSession{cipher: candidate.cipher, hello: candidate.hello, reply: reply, created: candidate.created}
			delete(pending, id)
			if tracker, ok := link.(interface{ trackHandshake(SessionID, bool) }); ok {
				tracker.trackHandshake(id, false)
			}
			if err = link.Send(ctx, reply, true); err != nil && !errors.Is(err, ErrCapacity) {
				return err
			}
			if options.Connected != nil {
				options.Connected(active)
			}
		}
	}
}
