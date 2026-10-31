package mailstream

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"
)

// Link is a bounded mailbox message service, not an authenticated connection.
// Send must return ErrCapacity instead of blocking behind a full DATA writer.
// priority selects the independent control queue. Attach prepares the new
// session's inbound DATA log without scanning any previous session's DATA.
type Link interface {
	Send(context.Context, []byte, bool) error
	Receive() <-chan []byte
	Attach(context.Context, SessionID) error
	Close() error
}

const (
	maxStreams        = 64
	streamWindow      = 4 << 20
	maxPendingBytes   = 32 << 20
	maxPendingPackets = 512
	controlReserve    = 64
	replaySlots       = 4096
)

type pendingPacket struct {
	wire     []byte
	priority bool
	sent     time.Time
	attempts int
}

type Session struct {
	ctx            context.Context
	cancel         context.CancelFunc
	link           Link
	cipher         *cipherPair
	server         bool
	dial           func(context.Context, string, string) (net.Conn, error)
	mu             sync.Mutex
	streams        map[uint32]*Stream
	closedStreams  map[uint32]bool
	nextStream     uint32
	pending        map[uint64]*pendingPacket
	pendingBytes   int
	changed        chan struct{}
	wake           chan struct{}
	input          chan []byte
	receipts       chan uint64
	seen           [replaySlots]uint64
	seenValid      [replaySlots]bool
	highest        uint64
	rtt, variation time.Duration
	pings          map[uint64]chan struct{}
	nextPing       uint64
	err            error
	wg             sync.WaitGroup
}

func newSession(parent context.Context, link Link, cipher *cipherPair, server bool, dial func(context.Context, string, string) (net.Conn, error)) *Session {
	ctx, cancel := context.WithCancel(parent)
	if dial == nil {
		dial = publicDial
	}
	s := &Session{ctx: ctx, cancel: cancel, link: link, cipher: cipher, server: server, dial: dial, streams: map[uint32]*Stream{}, nextStream: 1, pending: map[uint64]*pendingPacket{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), input: make(chan []byte, 512), receipts: make(chan uint64, 1024), pings: map[uint64]chan struct{}{}}
	s.closedStreams = map[uint32]bool{}
	// Sequence zero was the authenticated READY/READY_ACK. It must never
	// enter the application engine as a replayed session transition.
	s.seen[0], s.seenValid[0] = 0, true
	s.wg.Add(4)
	go func() { defer s.wg.Done(); <-ctx.Done(); s.fail(ErrClosed) }()
	go func() { defer s.wg.Done(); s.receiveLoop() }()
	go func() { defer s.wg.Done(); s.dispatchLoop() }()
	go func() { defer s.wg.Done(); s.receiptLoop() }()
	if storage, ok := link.(StorageLink); ok {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for {
				select {
				case <-s.ctx.Done():
					return
				case retired := <-storage.Retirements():
					if retired.Session != s.cipher.id {
						continue
					}
					if err := s.send(s.ctx, frame{kind: frameRetired, offset: uint64(retired.Generation), data: []byte(retired.Account)}, true); err != nil {
						return
					}
				}
			}
		}()
	}
	return s
}

func (s *Session) Done() <-chan struct{} { return s.ctx.Done() }
func (s *Session) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *Session) Close() error          { s.fail(ErrClosed); s.wg.Wait(); return nil }
func (s *Session) fail(err error) {
	s.mu.Lock()
	if s.err != nil {
		s.mu.Unlock()
		return
	}
	s.err = err
	s.cancel()
	close(s.changed)
	s.changed = make(chan struct{})
	streams := make([]*Stream, 0, len(s.streams))
	for _, stream := range s.streams {
		streams = append(streams, stream)
	}
	s.mu.Unlock()
	for _, stream := range streams {
		stream.abort(err)
	}
}
func (s *Session) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Session) send(ctx context.Context, f frame, priority bool) error {
	return s.enqueue(ctx, f, priority, true)
}
func (s *Session) trySend(f frame, priority bool) error { return s.enqueue(s.ctx, f, priority, false) }
func (s *Session) enqueue(ctx context.Context, f frame, priority, wait bool) error {
	plain, err := f.encode()
	if err != nil {
		return err
	}
	for {
		s.mu.Lock()
		if s.err != nil {
			err = s.err
			s.mu.Unlock()
			return err
		}
		limit := maxPendingPackets
		if priority {
			limit += controlReserve
		}
		byteLimit := maxPendingBytes
		if priority {
			byteLimit += controlReserve * 1024
		}
		if len(s.pending) < limit && s.pendingBytes+len(plain)+headerSize+16 <= byteLimit {
			wire, seq, err := s.cipher.seal(plain)
			if err == nil {
				s.pending[seq] = &pendingPacket{wire: wire, priority: priority}
				s.pendingBytes += len(wire)
				s.notifyLocked()
			}
			s.mu.Unlock()
			return err
		}
		change := s.changed
		s.mu.Unlock()
		if !wait {
			return ErrCapacity
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.ctx.Done():
			return ErrClosed
		case <-change:
		}
	}
}

func (s *Session) receiptLoop() {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var batch []uint64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		body := make([]byte, 8*len(batch))
		for n, seq := range batch {
			binary.BigEndian.PutUint64(body[n*8:], seq)
		}
		plain, _ := (frame{kind: frameReceipt, data: body}).encode()
		wire, _, err := s.cipher.seal(plain)
		if err != nil {
			return err
		}
		// Receipts are untracked and cannot consume the DATA pending budget.
		// An exact duplicate DATA packet causes its receipt to be sent again.
		err = s.link.Send(s.ctx, wire, true)
		batch = batch[:0]
		if errors.Is(err, ErrCapacity) {
			return nil
		}
		return err
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case seq := <-s.receipts:
			if len(batch) == 0 {
				timer.Reset(2 * time.Millisecond)
			}
			batch = append(batch, seq)
			if len(batch) == 32 {
				if err := flush(); err != nil {
					s.fail(err)
					return
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		case <-timer.C:
			if err := flush(); err != nil {
				s.fail(err)
				return
			}
		}
	}
}

func (s *Session) dispatchLoop() {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		for range 32 {
			now := time.Now()
			s.mu.Lock()
			rto := max(750*time.Millisecond, 2*s.rtt+4*s.variation)
			rto = min(8*time.Second, rto)
			var selected *pendingPacket
			var id uint64
			for seq, p := range s.pending {
				if !p.sent.IsZero() && now.Sub(p.sent) < rto {
					continue
				}
				if selected == nil || p.priority && !selected.priority || p.priority == selected.priority && seq < id {
					selected, id = p, seq
				}
			}
			if selected == nil {
				s.mu.Unlock()
				break
			}
			if selected.attempts >= 8 {
				s.mu.Unlock()
				s.fail(errors.New("MAILSTREAM_DELIVERY_TIMEOUT"))
				return
			}
			wire, priority := selected.wire, selected.priority
			s.mu.Unlock()
			err := s.link.Send(s.ctx, wire, priority)
			if errors.Is(err, ErrCapacity) {
				break
			}
			if err != nil {
				s.fail(err)
				return
			}
			s.mu.Lock()
			if p := s.pending[id]; p == selected {
				p.sent = now
				p.attempts++
			}
			s.mu.Unlock()
		}
	}
}

func (s *Session) receiveLoop() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case wire := <-s.input:
			plain, seq, err := s.cipher.open(wire)
			if err != nil {
				continue
			} // unrelated/unauthenticated mail is not a peer failure
			f, err := decodeFrame(plain)
			if err != nil {
				s.fail(err)
				return
			}
			if f.kind == frameReceipt {
				if f.stream != 0 || f.offset != 0 || len(f.data) == 0 || len(f.data) > 256 || len(f.data)%8 != 0 {
					s.fail(ErrProtocol)
					return
				}
				var acknowledged []uint64
				s.mu.Lock()
				for at := 0; at < len(f.data); at += 8 {
					id := binary.BigEndian.Uint64(f.data[at:])
					acknowledged = append(acknowledged, id)
					p := s.pending[id]
					if p == nil {
						continue
					}
					if p.attempts == 1 && !p.sent.IsZero() {
						sample := time.Since(p.sent)
						if s.rtt == 0 {
							s.rtt, s.variation = sample, sample/2
						} else {
							delta := s.rtt - sample
							if delta < 0 {
								delta = -delta
							}
							s.variation = (3*s.variation + delta) / 4
							s.rtt = (7*s.rtt + sample) / 8
						}
					}
					s.pendingBytes -= len(p.wire)
					delete(s.pending, id)
				}
				s.notifyLocked()
				s.mu.Unlock()
				if storage, ok := s.link.(StorageLink); ok {
					for _, id := range acknowledged {
						storage.Acknowledge(s.cipher.id, id)
					}
				}
				continue
			}
			// ACK traffic has its own nonces and can overtake an old DATA
			// packet by more than a cache's width. Eviction must not discard
			// legitimate outstanding DATA. Stream operations are idempotent
			// after this bounded duplicate cache rolls.
			duplicate := s.seenValid[seq%replaySlots] && s.seen[seq%replaySlots] == seq
			if !duplicate {
				if err = s.apply(f); errors.Is(err, ErrCapacity) {
					continue
				} else if err != nil {
					s.fail(err)
					return
				}
				s.seen[seq%replaySlots], s.seenValid[seq%replaySlots] = seq, true
				s.highest = max(s.highest, seq)
			}
			select {
			case s.receipts <- seq:
			case <-s.ctx.Done():
				return
			}
		}
	}
}

func (s *Session) apply(f frame) error {
	if f.kind == frameRetired {
		if f.stream != 0 || f.offset > uint64(^uint32(0)) || !validIdentity(string(f.data)) {
			return ErrProtocol
		}
		if storage, ok := s.link.(StorageLink); ok {
			return storage.ConfirmRetired(Retirement{Session: s.cipher.id, Account: string(f.data), Generation: uint32(f.offset)})
		}
		return ErrProtocol
	}
	if f.kind == framePing || f.kind == framePong {
		if f.stream != 0 || len(f.data) != 0 {
			return ErrProtocol
		}
		if f.kind == framePing {
			return s.trySend(frame{kind: framePong, offset: f.offset}, true)
		}
		s.mu.Lock()
		if pong := s.pings[f.offset]; pong != nil {
			close(pong)
			delete(s.pings, f.offset)
		}
		s.mu.Unlock()
		return nil
	}
	if f.stream == 0 || f.stream%2 == 0 {
		return ErrProtocol
	}
	if f.stream > 131071 {
		return ErrProtocol
	}
	s.mu.Lock()
	stream := s.streams[f.stream]
	closed := s.closedStreams[f.stream]
	if stream == nil && f.kind == frameReset {
		s.closedStreams[f.stream] = true
	}
	s.mu.Unlock()
	if closed {
		if f.kind == frameReset {
			return nil
		}
		return s.trySend(frame{kind: frameReset, stream: f.stream}, true)
	}
	if f.kind == frameOpen {
		if !s.server || f.offset != 0 || len(f.data) == 0 || len(f.data) > 256 {
			return ErrProtocol
		}
		if _, _, err := net.SplitHostPort(string(f.data)); err != nil {
			return ErrProtocol
		}
		if stream != nil {
			return nil
		}
		s.mu.Lock()
		if len(s.streams) >= maxStreams {
			s.mu.Unlock()
			return s.trySend(frame{kind: frameReset, stream: f.stream}, true)
		}
		stream = newStream(s, f.stream)
		s.streams[f.stream] = stream
		s.mu.Unlock()
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.serveStream(stream, string(f.data)) }()
		return nil
	}
	if stream == nil {
		if f.kind == frameReset {
			return nil
		}
		return s.trySend(frame{kind: frameReset, stream: f.stream}, true)
	}
	return stream.accept(f)
}

func (s *Session) serveStream(stream *Stream, target string) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	conn, err := s.dial(ctx, "tcp", target)
	if err != nil {
		stream.Close()
		return
	}
	defer conn.Close()
	if err = s.send(s.ctx, frame{kind: frameOpenOK, stream: stream.id}, true); err != nil {
		stream.Close()
		return
	}
	stream.markOpen()
	bridge(stream, conn)
	stream.Close()
}

func (s *Session) OpenTCP(ctx context.Context, target string) (net.Conn, error) {
	if s.server || len(target) > 256 {
		return nil, ErrProtocol
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		return nil, ErrProtocol
	}
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return nil, err
	}
	if len(s.streams) >= maxStreams || s.nextStream > 131071 {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	stream := newStream(s, s.nextStream)
	s.nextStream += 2
	s.streams[stream.id] = stream
	s.mu.Unlock()
	if err := s.send(ctx, frame{kind: frameOpen, stream: stream.id, data: []byte(target)}, true); err != nil {
		stream.Close()
		return nil, err
	}
	select {
	case <-ctx.Done():
		stream.Close()
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, ErrClosed
	case <-stream.opened:
	}
	stream.mu.Lock()
	err := stream.err
	stream.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return stream, nil
}

func (s *Session) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	s.mu.Lock()
	if len(s.pings) >= 64 {
		s.mu.Unlock()
		return 0, ErrCapacity
	}
	s.nextPing++
	id := s.nextPing
	done := make(chan struct{})
	s.pings[id] = done
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pings, id); s.mu.Unlock() }()
	if err := s.send(ctx, frame{kind: framePing, offset: id}, true); err != nil {
		return 0, err
	}
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.ctx.Done():
		return 0, ErrClosed
	case <-done:
		return time.Since(start), nil
	}
}

type Snapshot struct {
	Streams        int   `json:"streams"`
	PendingPackets int   `json:"pending_packets"`
	PendingBytes   int   `json:"pending_bytes"`
	DeliveryRTTMS  int64 `json:"delivery_rtt_ms"`
	Closed         bool  `json:"closed"`
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{len(s.streams), len(s.pending), s.pendingBytes, s.rtt.Milliseconds(), s.err != nil}
}
