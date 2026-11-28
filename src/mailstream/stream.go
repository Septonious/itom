package mailstream

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"itom.local/mailstream/v160"
)

// Each stream has independent receive storage and absolute consumed-byte
// credit. The mail receive owner never waits for an application Read or dial.
type Stream struct {
	ctx                                  context.Context
	cancel                               context.CancelFunc
	session                              *Session
	id                                   uint32
	readMu, writeMu                      sync.Mutex
	mu                                   sync.Mutex
	changed                              chan struct{}
	opened                               chan struct{}
	isOpen                               bool
	receive                              v160.Reassembler
	sendOffset, peerConsumed, advertised uint64
	writeClosed                          bool
	err                                  error
	readDeadline, writeDeadline          time.Time
	bulkWriteCompleted, writeBurstAt     time.Time // owned by writeMu
	writeBurstBytes                      int       // owned by writeMu
	receiveLimit, peerWindow             int
	gapReportedOffset                    uint64
	gapReportedAt                        time.Time
	gapReports                           int
	closeOnce                            sync.Once
}
type closeState uint8

const (
	closedAbort closeState = iota + 1
	closedGraceful
)

func (s *Stream) StreamID() uint64 { return uint64(s.id) }

func newStream(session *Session, id uint32) *Stream {
	ctx, cancel := context.WithCancel(session.ctx)
	return &Stream{ctx: ctx, cancel: cancel, session: session, id: id, changed: make(chan struct{}), opened: make(chan struct{}), receiveLimit: initialStreamWindow, peerWindow: initialStreamWindow}
}
func (s *Stream) notifyLocked() { close(s.changed); s.changed = make(chan struct{}) }
func (s *Stream) markOpen() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isOpen {
		s.isOpen = true
		close(s.opened)
	}
	s.notifyLocked()
}
func (s *Stream) abort(err error) {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
		s.discardReceiveLocked()
		if !s.isOpen {
			s.isOpen = true
			close(s.opened)
		}
		s.notifyLocked()
	}
}
func (s *Stream) accept(f frame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f.kind == frameWindow || f.kind == frameReset {
		s.session.mu.Lock()
		terminal := s.session.closedStreams[s.id]
		if terminal != nil {
			err := s.session.closedFrameLocked(terminal, f)
			s.session.mu.Unlock()
			return err
		}
		s.session.mu.Unlock()
	}
	if s.err != nil && f.kind != frameWindow && f.kind != frameReset {
		return nil
	}
	switch f.kind {
	case frameOpenOK:
		if s.session.server || f.offset != 0 || len(f.data) != 0 {
			return ErrProtocol
		}
		if !s.isOpen {
			s.isOpen = true
			close(s.opened)
		}
	case frameData:
		before := s.receive.Stats()
		now := time.Now()
		n, err := s.receive.Add(f.offset, f.data, s.receiveLimit, now, s.session.reserveReceive)
		if err != nil {
			return reassemblyError(err)
		}
		s.updateReassemblyLocked(before, now, false)
		s.session.receivedPayload.Add(uint64(n))
	case frameWindow:
		if len(f.data) != 4 || f.offset > s.sendOffset {
			return ErrProtocol
		}
		window := int(binary.BigEndian.Uint32(f.data))
		if window < initialStreamWindow || window > streamWindow {
			return ErrProtocol
		}
		s.peerWindow = max(s.peerWindow, window)
		if err := s.consumedLocked(f.offset); err != nil {
			return err
		}
	case frameFIN:
		if len(f.data) != 0 {
			return ErrProtocol
		}
		before, now := s.receive.Stats(), time.Now()
		if err := s.receive.SetFinal(f.offset, s.receiveLimit, now); err != nil {
			return reassemblyError(err)
		}
		s.updateReassemblyLocked(before, now, false)
	case frameReset:
		if f.offset != 0 || len(f.data) != 0 {
			return ErrProtocol
		}
		s.cancel()
		s.err = ErrClosed
		s.session.abandonStream(s.id)
		if err := s.consumedLocked(s.sendOffset); err != nil {
			return err
		}
		s.discardReceiveLocked()
		if !s.isOpen {
			s.isOpen = true
			close(s.opened)
		}
	default:
		return ErrProtocol
	}
	s.notifyLocked()
	return nil
}

func waitStream(ctx context.Context, change, credit <-chan struct{}, deadline time.Time) error {
	var timer *time.Timer
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		if !deadline.After(time.Now()) {
			return os.ErrDeadlineExceeded
		}
		timer = time.NewTimer(time.Until(deadline))
		timeout = timer.C
		defer timer.Stop()
	}
	select {
	case <-ctx.Done():
		return ErrClosed
	case <-timeout:
		return os.ErrDeadlineExceeded
	case <-change:
		return nil
	case <-credit:
		return nil
	}
}
func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	for {
		s.mu.Lock()
		if s.err != nil {
			err := s.err
			s.mu.Unlock()
			return 0, err
		}
		if !s.readDeadline.IsZero() && !s.readDeadline.After(time.Now()) {
			s.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		before := s.receive.Stats()
		n, err := s.receive.Read(p)
		if err != nil {
			s.mu.Unlock()
			return n, err
		}
		if n > 0 {
			s.session.receiveBuffered.Add(-int64(n))
			s.session.consumedPayload.Add(uint64(n))
			s.updateReassemblyLocked(before, time.Now(), false)
			consumed := before.Consumed + uint64(n)
			grew := false
			if consumed >= 64<<10 && s.receiveLimit == initialStreamWindow {
				s.receiveLimit = streamWindow
				grew = true
			}
			// The first window expansion remains immediate. Once bulk is
			// established, amortize credit announcements across 512 KiB.
			// Even 64 active streams leave at most 32 MiB unannounced,
			// below the shared 60 MiB bulk allowance. Close reports the tail.
			// A bearer transition may race with a WINDOW threshold choice. Both
			// thresholds are legal; no session mutex is needed for this hint.
			mobile := s.session.mobileBearer.Load()
			threshold := uint64(2 * initialStreamWindow)
			if mobile {
				threshold = initialStreamWindow / 2
			}
			sendWindow := grew || consumed-s.advertised >= threshold
			if sendWindow {
				s.advertised = consumed
			}
			window := s.receiveLimit
			s.mu.Unlock()
			if sendWindow {
				if err := s.session.publishWindow(s.id, consumed, window); err != nil {
					s.session.fail(err)
					return n, err
				}
			}
			return n, nil
		}
		change, deadline := s.changed, s.readDeadline
		s.mu.Unlock()
		if err := waitStream(s.session.ctx, change, nil, deadline); err != nil {
			return 0, err
		}
	}
}
func (s *Stream) Write(p []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		s.mu.Lock()
		if s.err != nil || s.writeClosed {
			err := s.err
			if err == nil {
				err = io.ErrClosedPipe
			}
			s.mu.Unlock()
			return written, err
		}
		if !s.writeDeadline.IsZero() && !s.writeDeadline.After(time.Now()) {
			s.mu.Unlock()
			return written, os.ErrDeadlineExceeded
		}
		s.session.mu.Lock()
		room := streamSendRoom(s.peerWindow, s.sendOffset-s.peerConsumed, s.session.delivery.mobile.enabled)
		if s.session.delivery.mobile.enabled {
			room = min(room, mobileStreamRoom(s.sendOffset-s.peerConsumed, s.session.delivery.mobile.pacing(time.Now())))
		}
		credit := s.session.changed
		if s.sendOffset >= streamPrefix {
			room = min(room, bulkBudget-s.session.bulkOutstanding)
		} else {
			room = min(room, streamPrefix-s.sendOffset)
		}
		if room == 0 {
			change, deadline := s.changed, s.writeDeadline
			s.session.mu.Unlock()
			s.mu.Unlock()
			if err := waitStream(s.session.ctx, change, credit, deadline); err != nil {
				return written, err
			}
			continue
		}
		payloadLimit := maxPayload - 13
		if s.session.delivery.mobile.enabled {
			payloadLimit = mobilePayloadLimit(s.session.delivery.mobile.pacing(time.Now()))
		}
		n := min(len(p), payloadLimit, int(room))
		offset := s.sendOffset
		deadline := s.writeDeadline
		if offset > ^uint64(0)-uint64(n) {
			s.session.mu.Unlock()
			s.mu.Unlock()
			return written, ErrCapacity
		}
		// Reserve the offset before publication: a fast peer's WINDOW may
		// arrive before send returns. Write owns this reservation exclusively.
		s.sendOffset += uint64(n)
		s.session.bulkOutstanding += bulkBytes(s.sendOffset) - bulkBytes(offset)
		s.session.mu.Unlock()
		s.mu.Unlock()
		ctx := s.session.ctx
		cancel := func() {}
		if !deadline.IsZero() {
			ctx, cancel = context.WithDeadline(ctx, deadline)
		}
		// Tiny TLS/HTTP framing writes adjacent to a bulk write belong
		// to its DATA batch. Treating every trailer as interactive filled
		// the control writer with bulk framing. An isolated small write
		// still dispatches immediately; this test never waits or buffers.
		now := time.Now()
		if now.Sub(s.writeBurstAt) > 32*time.Millisecond {
			s.writeBurstBytes = 0
		}
		// TLS dynamic record sizing starts with several small records. They
		// are one byte burst, not independent interactive requests to mirror.
		// A quiet stream's first 4 KiB remains immediate; paced small messages
		// reset the short burst counter and retain their interactive treatment.
		priority := n <= 4096 && s.writeBurstBytes+n <= 4096 && now.Sub(s.bulkWriteCompleted) > 250*time.Millisecond
		err := s.session.send(ctx, frame{kind: frameData, stream: s.id, offset: offset, data: p[:n]}, priority)
		cancel()
		if err != nil {
			s.mu.Lock()
			s.session.mu.Lock()
			if s.session.closedStreams[s.id] == nil {
				old := bulkBytes(s.sendOffset) - bulkBytes(s.peerConsumed)
				s.sendOffset = offset
				s.peerConsumed = min(s.peerConsumed, offset)
				remaining := bulkBytes(s.sendOffset) - bulkBytes(s.peerConsumed)
				s.session.bulkOutstanding -= old - remaining
				s.session.notifyLocked()
			}
			s.session.mu.Unlock()
			s.mu.Unlock()
			s.abort(err)
			return written, err
		}
		written += n
		s.writeBurstAt = time.Now()
		s.writeBurstBytes = min(4097, s.writeBurstBytes+n)
		if !priority {
			s.bulkWriteCompleted = time.Now()
		}
		p = p[n:]
	}
	return written, nil
}
func (s *Stream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	if s.writeClosed {
		s.mu.Unlock()
		return nil
	}
	s.writeClosed = true
	offset := s.sendOffset
	s.mu.Unlock()
	return s.session.send(s.session.ctx, frame{kind: frameFIN, stream: s.id, offset: offset}, true)
}
func (s *Stream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.mu.Lock()
		graceful := s.err == nil && s.writeClosed && s.receive.Finished()
		peerReset := errors.Is(s.err, ErrClosed)
		consumed, window := s.receive.Stats().Consumed, s.receiveLimit
		terminal := &terminalStream{state: closedAbort, sent: s.sendOffset, consumed: s.peerConsumed}
		if graceful {
			terminal.state = closedGraceful
		}
		if s.err == nil {
			s.err = ErrClosed
			s.discardReceiveLocked()
			if !s.isOpen {
				s.isOpen = true
				close(s.opened)
			}
			s.notifyLocked()
		}
		s.session.mu.Lock()
		delete(s.session.streams, s.id)
		s.session.closedStreams[s.id] = terminal
		s.session.notifyLocked()
		s.session.mu.Unlock()
		s.mu.Unlock()
		if graceful {
			if err := s.session.publishWindow(s.id, consumed, window); err != nil {
				s.session.fail(err)
			}
		}
		if !graceful {
			if s.session.windows != nil {
				s.session.windows.discard(s.id)
			}
			s.session.abandonStream(s.id)
		}
		if !graceful && !peerReset {
			ctx, cancel := context.WithTimeout(s.session.ctx, time.Second)
			defer cancel()
			_ = s.session.send(ctx, frame{kind: frameReset, stream: s.id}, true)
		}
	})
	return nil
}

type streamAddr string

func (a streamAddr) Network() string   { return "mailstream" }
func (a streamAddr) String() string    { return string(a) }
func (s *Stream) LocalAddr() net.Addr  { return streamAddr("local") }
func (s *Stream) RemoteAddr() net.Addr { return streamAddr("peer") }
func (s *Stream) SetDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline, s.writeDeadline = t, t
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}
func (s *Stream) SetReadDeadline(t time.Time) error {
	s.mu.Lock()
	s.readDeadline = t
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}
func (s *Stream) SetWriteDeadline(t time.Time) error {
	s.mu.Lock()
	s.writeDeadline = t
	s.notifyLocked()
	s.mu.Unlock()
	return nil
}

func bridge(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyOne := func(dst, src net.Conn) {
		defer wg.Done()
		_, err := io.Copy(dst, src)
		if err == nil || errors.Is(err, io.EOF) {
			if closer, ok := dst.(interface{ CloseWrite() error }); ok {
				_ = closer.CloseWrite()
			}
		} else {
			a.Close()
			b.Close()
		}
	}
	go copyOne(a, b)
	go copyOne(b, a)
	wg.Wait()
}

// Never revoke outstanding credit on a bearer handover; block new writes until consumption catches up.
func streamSendRoom(peerWindow int, outstanding uint64, mobile bool) uint64 {
	if mobile {
		peerWindow = min(peerWindow, 1<<20)
	}
	if outstanding >= uint64(peerWindow) {
		return 0
	}
	return uint64(peerWindow) - outstanding
}
func mobilePayloadLimit(rate float64) int { return min(maxPayload-13, max(16<<10, int(rate*.075))) }

// Limit cellular application read-ahead even when the peer advertised a large
// window. A cap reduction never revokes accepted data, only blocks new writes.
func mobileStreamRoom(outstanding uint64, rate float64) uint64 {
	limit := uint64(min(512<<10, max(256<<10, int(rate*.25))))
	if outstanding >= limit {
		return 0
	}
	return limit - outstanding
}

func reassemblyError(err error) error {
	if errors.Is(err, v160.ErrCapacity) {
		return ErrCapacity
	}
	return ErrProtocol
}

// Stream.mu protects the reassembly state. Aggregated counters are atomic so
// session snapshots do not reverse the Stream.mu -> Session.mu lock order.
func (s *Stream) updateReassemblyLocked(before v160.ReassemblyStats, now time.Time, discard bool) {
	after := s.receive.Stats()
	sess := s.session
	sess.readyBuffered.Add(int64(after.Ready - before.Ready))
	sess.gapBuffered.Add(int64(after.Blocked - before.Blocked))
	sess.duplicatePayload.Add(after.DuplicateBytes - before.DuplicateBytes)
	if !before.GapSince.IsZero() && (after.GapSince.IsZero() || after.GapOffset != before.GapOffset) {
		if discard {
			sess.gapAborted.Add(1)
		} else {
			sess.gapResolved.Add(1)
			sess.gapWaitNS.Add(uint64(max(0, now.Sub(before.GapSince))))
		}
	}
	if !after.GapSince.IsZero() && (before.GapSince.IsZero() || after.GapOffset != before.GapOffset) {
		sess.gapOpened.Add(1)
	}
}
func (s *Stream) discardReceiveLocked() {
	before := s.receive.Stats()
	n := s.receive.Discard()
	s.session.receiveBuffered.Add(-int64(n))
	s.updateReassemblyLocked(before, time.Now(), true)
}
