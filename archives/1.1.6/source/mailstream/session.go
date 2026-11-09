package mailstream

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"slices"
	"sync"
	"sync/atomic"
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
	maxStreams          = 64
	streamWindow        = 32 << 20
	initialStreamWindow = 256 << 10
	maxPendingBytes     = 32 << 20
	// Bound bytes independently of packet count: TLS/HTTP framing can
	// otherwise exhaust 512 slots with only a few MiB actually in flight.
	maxPendingPackets       = 2048
	controlReserve          = 64
	interactiveReserveBytes = controlReserve * (4096 + 13 + headerSize + 16)
	replaySlots             = 4096
)

type pendingPacket struct {
	deliveryMark deliveryMark
	wire         []byte
	priority     bool
	sent         time.Time
	attempts     int
	kind         byte
	stream       uint32
}

type Session struct {
	ctx             context.Context
	cancel          context.CancelFunc
	link            Link
	cipher          *cipherPair
	server          bool
	dial            func(context.Context, string, string) (net.Conn, error)
	mu              sync.Mutex
	streams         map[uint32]*Stream
	closedStreams   map[uint32]*terminalStream
	bulkOutstanding uint64
	receiveBuffered atomic.Int64
	lastInbound     atomic.Int64
	nextStream      uint32
	pending         map[uint64]*pendingPacket
	pendingBytes    int
	delivery        deliveryWindow
	changed         chan struct{}
	wake            chan struct{}
	input           chan []byte
	receipts        chan receiptRequest
	receiptRetries  atomic.Uint64
	openRefusals    atomic.Uint64
	openWaits       atomic.Uint64
	retransmissions uint64
	seen            [replaySlots]uint64
	seenValid       [replaySlots]bool
	highest         uint64
	rtt, variation  time.Duration
	pings           map[uint64]chan struct{}
	nextPing        uint64
	err             error
	wg              sync.WaitGroup
}

func newSession(parent context.Context, link Link, cipher *cipherPair, server bool, dial func(context.Context, string, string) (net.Conn, error)) *Session {
	ctx, cancel := context.WithCancel(parent)
	if dial == nil {
		dial = publicDial
	}
	s := &Session{ctx: ctx, cancel: cancel, link: link, cipher: cipher, server: server, dial: dial, streams: map[uint32]*Stream{}, nextStream: 1, pending: map[uint64]*pendingPacket{}, changed: make(chan struct{}), wake: make(chan struct{}, 1), input: make(chan []byte, 512), receipts: make(chan receiptRequest, 1024), pings: map[uint64]chan struct{}{}}
	s.delivery.adaptive = true // Both directions use compression-safe bulk feedback.
	s.closedStreams = map[uint32]*terminalStream{}
	s.lastInbound.Store(time.Now().UnixNano())
	// Sequence zero was the authenticated READY/READY_ACK. It must never
	// enter the application engine as a replayed session transition.
	s.seen[0], s.seenValid[0] = 0, true
	s.wg.Add(4)
	go func() { defer s.wg.Done(); <-ctx.Done(); s.fail(ErrClosed) }()
	go func() { defer s.wg.Done(); s.receiveLoop() }()
	go func() { defer s.wg.Done(); s.dispatchLoop() }()
	go func() { defer s.wg.Done(); s.receiptLoop() }()
	if routes, ok := link.(accountAnnouncer); ok {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.announceRoutes(routes) }()
	}
	if storage, ok := link.(StorageLink); ok {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			tick := time.NewTicker(250 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-tick.C:
					if maintenance, ok := link.(maintenanceLink); ok {
						for _, f := range maintenance.maintenanceFrames(s.cipher.id) {
							if err := s.send(s.ctx, f, true); err != nil {
								return
							}
						}
					}
				case <-s.ctx.Done():
					return
				case retired := <-storage.Retirements():
					if retired.Session != s.cipher.id {
						continue
					}
					kind := retired.Kind
					if kind == 0 {
						kind = frameRetired
					}
					data := []byte(retired.Account)
					if kind == frameSegment {
						data = binary.BigEndian.AppendUint32(data, retired.Slot)
					}
					if err := s.send(s.ctx, frame{kind: kind, offset: uint64(retired.Generation), data: data}, true); err != nil {
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
	// Only isolated small DATA may use the finite interactive reserve.
	// Large frames always retain the bulk admission and flight limits.
	if f.kind == frameData && len(f.data) > 4096 {
		priority = false
	}
	for {
		s.mu.Lock()
		if s.err != nil {
			err = s.err
			s.mu.Unlock()
			return err
		}
		if terminal := s.closedStreams[f.stream]; terminal != nil && terminal.state == closedAbort &&
			(f.kind == frameData || f.kind == frameFIN || f.kind == frameOpen || f.kind == frameOpenOK) {
			s.mu.Unlock()
			return ErrClosed
		}
		limit := maxPendingPackets
		byteLimit := min(maxPendingBytes, 2*s.delivery.allowance())
		if priority {
			limit += controlReserve
			byteLimit = maxPendingBytes + interactiveReserveBytes
			if f.kind != frameData {
				// Protocol credit/reset/reclamation must survive even a full
				// interactive reserve as well as a full bulk queue.
				limit += controlReserve
				byteLimit += controlReserve * 1024
			}
		}
		room := len(s.pending) < limit && s.pendingBytes+len(plain)+headerSize+16 <= byteLimit
		if room && priority && f.kind == frameData {
			interactive := 0
			for _, p := range s.pending {
				if p.priority && p.kind == frameData {
					interactive++
				}
			}
			room = interactive < controlReserve
		}
		if room {
			wire, seq, err := s.cipher.seal(plain)
			if err == nil {
				s.pending[seq] = &pendingPacket{wire: wire, priority: priority, kind: f.kind, stream: f.stream}
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

type receiptRequest struct {
	sequence uint64
	bulk     bool
}

func (s *Session) receiptLoop() {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	batch := make([]uint64, 0, 32)
	var deadline time.Time
	var wire []byte
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if wire == nil {
			body := make([]byte, 8*len(batch))
			for n, seq := range batch {
				binary.BigEndian.PutUint64(body[n*8:], seq)
			}
			plain, _ := (frame{kind: frameReceipt, data: body}).encode()
			var err error
			wire, _, err = s.cipher.seal(plain)
			if err != nil {
				return err
			}
		}
		// Local queue pressure is not packet loss. Keep this bounded,
		// immutable receipt until a writer accepts it instead of forcing
		// the peer to retransmit already delivered application data.
		var err error
		if feedback, ok := s.link.(interface {
			SendFeedback(context.Context, []byte) error
		}); ok {
			err = feedback.SendFeedback(s.ctx, wire)
		} else {
			err = s.link.Send(s.ctx, wire, true)
		}
		if errors.Is(err, ErrCapacity) {
			s.receiptRetries.Add(1)
			timer.Reset(2 * time.Millisecond)
			return nil
		}
		if err != nil {
			return err
		}
		wire = nil
		batch = batch[:0]
		return nil
	}
	for {
		input := s.receipts
		if wire != nil {
			input = nil
		} // freeze the encrypted batch during retry
		select {
		case <-s.ctx.Done():
			return
		case request := <-input:
			now := time.Now()
			if len(batch) == 0 {
				delay := 2 * time.Millisecond
				if request.bulk {
					delay = 8 * time.Millisecond
				}
				deadline = now.Add(delay)
				timer.Reset(delay)
			} else if !request.bulk {
				// Interactive/control receipts preempt a bulk-only batch.
				// Bulk still fits the unchanged32-sequence wire format.
				deadline = minTime(deadline, now.Add(2*time.Millisecond))
				timer.Reset(max(0, time.Until(deadline)))
			}
			batch = append(batch, request.sequence)
			if len(batch) == 32 {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if err := flush(); err != nil {
					s.fail(err)
					return
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

// These existing authenticated frames are required to release physical
// storage. They share the finite recovery reserve and precede ordinary packets
// when a mailbox has hit its independent message-count cap.
func storageControl(kind byte) bool {
	switch kind {
	case frameControlRead, frameControlCollected, frameStorageRecovered, frameStoragePressure, frameRetired, frameCollect, frameCollected, frameSegment:
		return true
	}
	return false
}

func (s *Session) dispatchLoop() {
	type candidate struct {
		sequence uint64
		packet   *pendingPacket
	}
	batch := make([]candidate, 0, maxPendingPackets+controlReserve)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		now := time.Now()
		clear(batch)
		batch = batch[:0]
		s.mu.Lock()
		rto := min(8*time.Second, max(750*time.Millisecond, 2*s.rtt+4*s.variation))
		for seq, p := range s.pending {
			if p.sent.IsZero() || now.Sub(p.sent) >= retryAfter(p, rto) {
				batch = append(batch, candidate{seq, p})
			}
		}
		s.mu.Unlock()
		// Scan once per wake, then order outside the session lock. Scanning
		// the entire map for every packet made a larger packet allowance
		// consume CPU and delay incoming credit/control under bulk load.
		slices.SortFunc(batch, func(a, b candidate) int {
			ah := storageControl(a.packet.kind)
			bh := storageControl(b.packet.kind)
			if ah != bh {
				if ah {
					return -1
				}
				return 1
			}
			if a.packet.priority != b.packet.priority {
				if a.packet.priority {
					return -1
				}
				return 1
			}
			return cmp.Compare(a.sequence, b.sequence)
		})
		sent := 0
		for _, next := range batch {
			if sent >= 32 {
				break
			}
			s.mu.Lock()
			selected := next.packet
			if s.pending[next.sequence] != selected {
				s.mu.Unlock()
				continue
			}
			if selected.kind == frameData && !selected.priority && selected.attempts == 0 && s.delivery.flight+len(selected.wire) > s.delivery.allowance() {
				s.delivery.limited = true
				s.mu.Unlock()
				continue
			}
			if selected.kind == frameData && !selected.priority {
				at := time.Now()
				if !s.delivery.pacer.ready(len(selected.wire), s.delivery.pacingRate(at), at) {
					// Priority traffic was sorted first and remains immediate.
					s.mu.Unlock()
					break
				}
			}
			if selected.attempts >= 8 && time.Since(time.Unix(0, s.lastInbound.Load())) > 2*time.Minute {
				s.mu.Unlock()
				s.fail(errors.New("MAILSTREAM_DELIVERY_TIMEOUT"))
				return
			}
			wire, priority := selected.wire, selected.priority
			interactive := priority && selected.attempts == 0 &&
				(selected.kind == frameData || selected.kind == frameOpen || selected.kind == frameOpenOK || selected.kind == framePing || selected.kind == framePong)
			s.mu.Unlock()
			var err error
			started := time.Now()
			maintenance, storage := s.link.(interface {
				SendHousekeeping(context.Context, []byte) error
			})
			if storage && (storageControl(selected.kind)) {
				err = maintenance.SendHousekeeping(s.ctx, wire)
			} else if feedback, ok := s.link.(interface {
				SendFeedback(context.Context, []byte) error
			}); ok && selected.kind == frameWindow {
				err = feedback.SendFeedback(s.ctx, wire)
			} else if fast, ok := s.link.(interface {
				SendInteractive(context.Context, []byte) error
			}); interactive && ok {
				err = fast.SendInteractive(s.ctx, wire)
			} else {
				err = s.link.Send(s.ctx, wire, priority)
			}
			if errors.Is(err, ErrCapacity) {
				break
			}
			if err != nil {
				s.fail(err)
				return
			}
			if observer, ok := s.link.(interface {
				TrackStream(SessionID, uint64, uint32)
			}); ok {
				observer.TrackStream(s.cipher.id, next.sequence, selected.stream)
			}
			s.mu.Lock()
			if selected.kind == frameData && !selected.priority {
				s.delivery.pacer.admitted(len(wire))
			}
			if p := s.pending[next.sequence]; p == selected {
				if p.attempts == 0 && p.kind == frameData {
					p.deliveryMark = s.delivery.sent(len(p.wire), started, !p.priority)
				}
				if p.attempts != 0 {
					s.retransmissions++
				}
				p.sent = started
				p.attempts++
			}
			s.mu.Unlock()
			sent++
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
			if confirmed, ok := s.link.(interface{ ConfirmPeer([]byte) }); ok {
				confirmed.ConfirmPeer(wire)
			}
			// Replayed ciphertext cannot prolong an abandoned server session's
			// physical connection reservation. This is a lifetime lease, not CC.
			if seq > s.highest {
				s.highest = seq
				s.lastInbound.Store(time.Now().UnixNano())
			}
			if f.kind == frameReceipt {
				if f.stream != 0 || f.offset != 0 || len(f.data) == 0 || len(f.data) > 256 || len(f.data)%8 != 0 {
					s.fail(ErrProtocol)
					return
				}
				var acknowledged []uint64
				var creditErr error
				s.mu.Lock()
				for at := 0; at < len(f.data); at += 8 {
					id := binary.BigEndian.Uint64(f.data[at:])
					acknowledged = append(acknowledged, id)
					p := s.pending[id]
					if p == nil {
						continue
					}
					if p.kind == frameData && p.attempts > 0 {
						sample := time.Duration(0)
						if p.attempts == 1 {
							sample = time.Since(p.sent)
						}
						s.delivery.delivered(len(p.wire), sample, time.Now(), !p.priority, p.deliveryMark)
					}
					if p.attempts == 1 && !p.sent.IsZero() {
						sample := time.Since(p.sent)
						if p.kind == framePing || p.kind == framePong {
							s.delivery.observeInteractive(sample, time.Now())
						}
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
					if p.kind == frameReset {
						creditErr = errors.Join(creditErr, s.releaseTerminalLocked(p.stream))
					}
					delete(s.pending, id)
				}
				s.notifyLocked()
				s.mu.Unlock()
				if creditErr != nil {
					s.fail(creditErr)
					return
				}
				if storage, ok := s.link.(StorageLink); ok {
					for _, id := range acknowledged {
						if err := storage.Acknowledge(s.cipher.id, id); err != nil {
							s.fail(err)
							return
						}
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
					s.fail(fmt.Errorf("MAILSTREAM_FRAME_%d: %w", f.kind, err))
					return
				}
				s.seen[seq%replaySlots], s.seenValid[seq%replaySlots] = seq, true
				s.highest = max(s.highest, seq)
			}
			select {
			case s.receipts <- receiptRequest{sequence: seq, bulk: f.kind == frameData && len(f.data) > 4096}:
			case <-s.ctx.Done():
				return
			}
		}
	}
}

func (s *Session) apply(f frame) error {
	if f.kind == frameControlRead || f.kind == frameControlCollected || f.kind == frameStorageRecovered || f.kind == frameStoragePressure {
		if maintenance, ok := s.link.(maintenanceLink); ok {
			return maintenance.applyMaintenance(s.cipher.id, f)
		}
		return ErrProtocol
	}
	if f.kind == frameRouteReady {
		if f.stream != 0 || f.offset != 0 || len(f.data) != 0 {
			return ErrProtocol
		}
		return nil
	}
	if f.kind == frameSegment {
		if f.stream != 0 || f.offset > uint64(^uint32(0)) || len(f.data) < 5 || !validIdentity(string(f.data[:len(f.data)-4])) {
			return ErrProtocol
		}
		if storage, ok := s.link.(StorageLink); ok {
			return storage.ConfirmRoute(Retirement{Session: s.cipher.id, Account: string(f.data[:len(f.data)-4]), Generation: uint32(f.offset), Slot: binary.BigEndian.Uint32(f.data[len(f.data)-4:])})
		}
		return ErrProtocol
	}
	if f.kind == frameRetired || f.kind == frameCollect || f.kind == frameCollected {
		if f.stream != 0 || f.offset > uint64(^uint32(0)) || !validIdentity(string(f.data)) {
			return ErrProtocol
		}
		storage, ok := s.link.(StorageLink)
		if !ok {
			return ErrProtocol
		}
		r := Retirement{Session: s.cipher.id, Account: string(f.data), Generation: uint32(f.offset)}
		switch f.kind {
		case frameRetired:
			return storage.ConfirmRetired(r)
		case frameCollect:
			return storage.RequestCollection(r)
		case frameCollected:
			return storage.ConfirmCollection(r)
		}
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
	if stream == nil && f.kind == frameReset && closed == nil {
		s.closedStreams[f.stream] = &terminalStream{state: closedAbort}
	}
	if closed != nil {
		err := s.closedFrameLocked(closed, f)
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
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
			// A close RESET can cross a new OPEN. Withhold its receipt
			// until a slot frees instead of aborting the new connection.
			return ErrCapacity
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
	ctx, cancel := context.WithCancel(stream.ctx)
	defer cancel()
	dialTimeout := time.AfterFunc(10*time.Second, cancel)
	conn, err := s.dial(ctx, "tcp", target)
	dialCompleted := dialTimeout.Stop()
	if err != nil || !dialCompleted {
		if conn != nil {
			conn.Close()
		}
		stream.Close()
		return
	}
	defer conn.Close()
	// An established exit socket must end on stream reset or session
	// replacement, including when one bridge leg already reached FIN.
	stopClose := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopClose()
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
	waited := false
	for {
		s.mu.Lock()
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return nil, err
		}
		if s.nextStream > 131071 {
			s.openRefusals.Add(1)
			s.mu.Unlock()
			return nil, ErrCapacity
		}
		if len(s.streams) < maxStreams {
			break
		}
		if !waited {
			s.openWaits.Add(1)
			waited = true
		}
		change := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.ctx.Done():
			return nil, ErrClosed
		case <-change:
		}
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
		stream.Close()
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
	BulkPacingRateBPS          int64  `json:"bulk_pacing_bytes_per_second"`
	BulkPacingDeferrals        uint64 `json:"bulk_pacing_deferrals"`
	InteractiveReceiptRTTMS    int64  `json:"interactive_receipt_rtt_ms"`
	InteractiveReceiptMinRTTMS int64  `json:"interactive_receipt_min_rtt_ms"`
	InteractiveQueueLimited    bool   `json:"interactive_queue_limited"`
	BulkSaturatedMinRTTMS      int64  `json:"bulk_saturated_round_rtt_ms"`
	BulkDeliveryHorizonMS      int64  `json:"bulk_delivery_horizon_ms"`
	BulkSmoothedRTTMS          int64  `json:"bulk_receipt_rtt_ms"`
	BulkReceiptRTTMS           int64  `json:"bulk_receipt_min_rtt_ms"`
	BulkDeliveryRateBPS        int64  `json:"bulk_delivery_bytes_per_second"`
	BulkWindowLimited          bool   `json:"bulk_window_limited"`
	OpenRefusals               uint64 `json:"open_refusals"`
	OpenWaits                  uint64 `json:"open_capacity_waits"`
	Streams                    int    `json:"streams"`
	PendingPackets             int    `json:"pending_packets"`
	PendingBytes               int    `json:"pending_bytes"`
	DeliveryRTTMS              int64  `json:"delivery_rtt_ms"`
	Closed                     bool   `json:"closed"`
	Failure                    string `json:"failure,omitempty"`
	ReceiptBackpressure        uint64 `json:"receipt_backpressure"`
	Retransmissions            uint64 `json:"retransmissions"`
	BulkOutstanding            uint64 `json:"bulk_outstanding_bytes"`
	ReceiveBuffered            int64  `json:"receive_buffered_bytes"`
	DeliveryFlight             int    `json:"delivery_flight_bytes"`
	DeliveryWindow             int    `json:"delivery_window_bytes"`
}

func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := Snapshot{BulkPacingRateBPS: int64(s.delivery.pacingRate(time.Now())), BulkPacingDeferrals: s.delivery.pacer.deferrals, InteractiveReceiptRTTMS: s.delivery.controlRTT.Milliseconds(), InteractiveReceiptMinRTTMS: s.delivery.controlBase.Milliseconds(), InteractiveQueueLimited: time.Now().Before(s.delivery.controlSlowUntil), BulkSaturatedMinRTTMS: s.delivery.saturatedBase.Milliseconds(), BulkDeliveryHorizonMS: s.delivery.horizon.Milliseconds(), BulkSmoothedRTTMS: s.delivery.bulkRTT.Milliseconds(), BulkReceiptRTTMS: s.delivery.base.Milliseconds(), BulkDeliveryRateBPS: int64(s.delivery.rate), BulkWindowLimited: s.delivery.limited && s.delivery.flight+maxPayload > s.delivery.allowance(), OpenRefusals: s.openRefusals.Load(), OpenWaits: s.openWaits.Load(), Streams: len(s.streams), PendingPackets: len(s.pending), PendingBytes: s.pendingBytes,
		DeliveryRTTMS: s.rtt.Milliseconds(), Closed: s.err != nil, BulkOutstanding: s.bulkOutstanding,
		ReceiveBuffered: s.receiveBuffered.Load(), ReceiptBackpressure: s.receiptRetries.Load(), Retransmissions: s.retransmissions,
		DeliveryFlight: s.delivery.flight, DeliveryWindow: s.delivery.allowance()}
	if s.err != nil {
		snapshot.Failure = s.err.Error()
	}
	return snapshot
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
