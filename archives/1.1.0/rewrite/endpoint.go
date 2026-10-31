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
				if err = link.Attach(ctx, h.id); err != nil {
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
				if err != nil || seq != 0 || f.kind != frameReadyAck || f.stream != 0 || f.offset != 0 || len(f.data) != 0 {
					continue
				}
				// Startup's caller deadline is not the established connection's
				// lifetime. The owner closes Session/Link explicitly.
				s := newSession(context.WithoutCancel(ctx), link, cipher, false, nil)
				s.wg.Add(1)
				go func() { defer s.wg.Done(); routeSession(s, link.Receive()) }()
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
	created      time.Time
}

// Serve owns the standing control receiver across user VPN restarts. A replayed
// HELLO cannot replace an established session: the new ephemeral Noise split
// key must authenticate READY before Attach selects its DATA namespace.
// The prototype serves one configured client identity per Link.
func Serve(ctx context.Context, link Link, identity string, psk []byte, dial func(context.Context, string, string) (net.Conn, error), connected func(*Session)) error {
	if len(psk) != 32 || len(identity) == 0 || len(identity) > 64 {
		return ErrProtocol
	}
	var active *Session
	var current *candidateSession
	pending := map[SessionID]*candidateSession{}
	defer func() {
		if active != nil {
			active.Close()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
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
					}
				}
				if len(pending) >= 16 {
					continue
				}
				cipher, reply, err := respond(psk, identity, packet)
				if err != nil {
					continue
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
			if err = link.Attach(ctx, id); err != nil {
				delete(pending, id)
				continue
			}
			if active != nil {
				active.Close()
			}
			active = newSession(ctx, link, candidate.cipher, true, dial)
			plain, _ := (frame{kind: frameReadyAck}).encode()
			reply, _, err := candidate.cipher.seal(plain)
			if err != nil {
				return err
			}
			current = &candidateSession{cipher: candidate.cipher, hello: candidate.hello, reply: reply, created: candidate.created}
			delete(pending, id)
			if err = link.Send(ctx, reply, true); err != nil && !errors.Is(err, ErrCapacity) {
				return err
			}
			if connected != nil {
				connected(active)
			}
		}
	}
}
