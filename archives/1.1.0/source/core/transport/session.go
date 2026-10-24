package transport

import (
	"bytes"
	"io"
	"sync"
	"time"
)

type Limits struct {
	Streams                   int
	SessionBytes, StreamBytes uint64
	InteractiveBytes          int
	LightBytesPerSecond       uint64
}

func DefaultLimits() Limits {
	return Limits{Streams: 128, SessionBytes: 64 << 20, StreamBytes: 8 << 20, InteractiveBytes: 32 << 10, LightBytesPerSecond: 64 << 10}
}

type sentChunk struct {
	record   Record
	outcome  PublicationOutcome
	returned bool
}
type stream struct {
	id                                                                     uint64
	tx                                                                     bytes.Buffer
	sent, peerCredit, recv, consumed, receipt                              uint64
	rx                                                                     bytes.Buffer
	reorder                                                                map[uint64][]byte
	reorderBytes, window, buffered                                         uint64
	pending                                                                []*sentChunk
	interactive                                                            interactiveBudget
	creditPending, receiptPending, finishAckPending, resetAckPending       bool
	localFinish, finishSent, finishAcked, finishAckPublished, remoteFinish bool
	localReset, remoteReset, resetSent, resetAcked, resetAckPublished      bool
	final                                                                  uint64
	notify                                                                 chan struct{}
	connection                                                             *Conn
	applicationClosed                                                      bool
}

func (f *stream) stopped() bool { return f.localReset || f.remoteReset }
func (f *stream) receiveLimit() uint64 {
	if f.consumed > maxStreamID-f.window {
		return maxStreamID
	}
	return f.consumed + f.window
}
func (f *stream) signal() { close(f.notify); f.notify = make(chan struct{}) }

// Session owns streams, byte ordering and credit independently of accounts.
// Bytes handed to a carrier remain charged to its bounded send memory until
// authenticated cumulative peer receipt. No disk/provider I/O runs under mu.
type Session struct {
	mu                           sync.Mutex
	generation                   uint64
	limits                       Limits
	flows                        map[uint64]*stream
	order                        []uint64
	cursor                       [2]int
	buffered, received, reserved uint64
	nextToken                    uint64
	records                      int
	dispatches                   map[uint64]*dispatchState
	retries                      [2][]Record
	outgoing, space              chan struct{}
	closed                       bool
	ids                          idWindow
	interactive                  interactiveBudget
}

func NewSession(generation uint64, l Limits) (*Session, error) {
	if generation == 0 || l.Streams < 1 || l.Streams > 4096 || l.StreamBytes == 0 || l.SessionBytes < l.StreamBytes || l.SessionBytes > 1<<30 || l.InteractiveBytes < 1 || l.InteractiveBytes > MaxPayload || l.LightBytesPerSecond == 0 || l.LightBytesPerSecond > 1<<30 {
		return nil, ErrCapacity
	}
	return &Session{generation: generation, limits: l, flows: make(map[uint64]*stream), dispatches: make(map[uint64]*dispatchState), outgoing: make(chan struct{}), space: make(chan struct{}), interactive: interactiveBudget{tokens: float64(l.InteractiveBytes)}}, nil
}
func (s *Session) signalOutgoing() { close(s.outgoing); s.outgoing = make(chan struct{}) }
func (s *Session) signalSpace()    { close(s.space); s.space = make(chan struct{}) }

// WorkChanged returns an edge notification, not evidence that a carrier is
// usable. The coordinator samples it before Take so it cannot miss a wakeup.
func (s *Session) WorkChanged() <-chan struct{} { s.mu.Lock(); defer s.mu.Unlock(); return s.outgoing }
func (s *Session) Add(id, initialCredit, receiveWindow uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if id == 0 || id > maxStreamID || len(s.flows) >= s.limits.Streams || initialCredit > s.limits.StreamBytes || receiveWindow == 0 || receiveWindow > s.limits.StreamBytes || receiveWindow > s.limits.SessionBytes-s.reserved {
		return ErrCapacity
	}
	if _, exists := s.flows[id]; exists || !s.ids.available(id) {
		return ErrProtocol
	}
	s.flows[id] = &stream{id: id, peerCredit: initialCredit, window: receiveWindow, interactive: interactiveBudget{tokens: float64(s.limits.InteractiveBytes)}, reorder: make(map[uint64][]byte), notify: make(chan struct{})}
	s.ids.commit(id)
	s.order = append(s.order, id)
	s.reserved += receiveWindow
	return nil
}
func (s *Session) queueLocked(id uint64, b []byte, now time.Time) error {
	f := s.flows[id]
	if s.closed || f == nil || f.stopped() || f.localFinish || f.applicationClosed {
		return ErrClosed
	}
	n := uint64(len(b))
	if n > s.limits.StreamBytes-f.buffered || n > s.limits.SessionBytes-s.buffered {
		return ErrCapacity
	}
	if uint64(f.tx.Len()) > maxStreamID-f.sent || n > maxStreamID-f.sent-uint64(f.tx.Len()) {
		return ErrProtocol
	}
	s.refillInteractiveLocked(now)
	f.tx.Write(b)
	f.buffered += n
	s.buffered += n
	s.signalOutgoing()
	return nil
}
func (s *Session) Queue(id uint64, b []byte, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queueLocked(id, b, now)
}
func (s *Session) Finish(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if s.closed || f == nil || f.stopped() {
		return ErrClosed
	}
	f.localFinish = true
	f.signal()
	s.signalOutgoing()
	return nil
}
func (s *Session) nextLocked(plane Plane, max int, class LedgerClass) (Record, bool) {
	if s.closed || plane > Bulk || max < 0 {
		return Record{}, false
	}
	if max > MaxPayload {
		max = MaxPayload
	}
	for attempts := len(s.retries[plane]); attempts > 0; attempts-- {
		r := s.retries[plane][0]
		s.retries[plane] = s.retries[plane][1:]
		f := s.flows[r.Stream]
		if f == nil {
			continue
		}
		if r.Kind == Data && (f.stopped() || r.Offset+uint64(len(r.Payload)) <= f.receipt) {
			continue
		}
		if !class.accepts(r.Kind) || r.Kind == Data && len(r.Payload) > max {
			s.retries[plane] = append(s.retries[plane], r)
			continue
		}
		return r, true
	}
	n := len(s.order)
	if n == 0 {
		return Record{}, false
	}
	for j := 0; j < n; j++ {
		i := (s.cursor[plane] + j) % n
		id := s.order[i]
		f := s.flows[id]
		r := Record{Generation: s.generation, Stream: id}
		if plane == Fast && class != DataLedger {
			switch {
			case f.resetAckPending:
				f.resetAckPending = false
				r.Kind = ResetAck
			case f.localReset && !f.resetSent:
				f.resetSent = true
				r.Kind = Reset
			case !f.stopped() && f.creditPending:
				f.creditPending = false
				f.receiptPending = false
				r.Kind = Credit
				r.Offset = f.receiveLimit()
				r.Acknowledged = f.recv
			case !f.stopped() && f.receiptPending:
				f.receiptPending = false
				r.Kind = Receipt
				r.Offset = f.recv
			case !f.stopped() && f.finishAckPending:
				f.finishAckPending = false
				r.Kind = FinishAck
				r.Offset = f.final
			case !f.stopped() && f.localFinish && f.tx.Len() == 0 && !f.finishSent:
				f.finishSent = true
				r.Kind = Finish
				r.Offset = f.sent
			}
			if r.Kind != 0 {
				s.cursor[plane] = (i + 1) % n
				return r, true
			}
		}
		if class == ControlLedger || max == 0 || f.stopped() || f.tx.Len() == 0 || f.sent >= f.peerCredit || len(f.pending) >= 4096 || s.records >= 16384 {
			continue
		}
		short := s.shortLocked(f)
		if (plane == Fast) != short {
			continue
		}
		take := f.tx.Len()
		if take > max {
			take = max
		}
		if uint64(take) > f.peerCredit-f.sent {
			take = int(f.peerCredit - f.sent)
		}
		r.Kind = Data
		r.Offset = f.sent
		r.Acknowledged = f.recv
		r.ReceiveLimit = f.receiveLimit()
		f.receiptPending = false
		f.creditPending = false
		r.Payload = append([]byte(nil), f.tx.Next(take)...)
		f.pending = append(f.pending, &sentChunk{record: r})
		s.records++
		f.sent += uint64(take)
		if plane == Fast {
			f.interactive.tokens -= float64(take)
			s.interactive.tokens -= float64(take)
		} else {
			f.interactive.tokens = 0
		}
		s.cursor[plane] = (i + 1) % n
		return r, true
	}
	return Record{}, false
}
func (s *Session) validReceiptLocked(f *stream, end uint64) bool {
	if end > f.sent {
		return false
	}
	if end <= f.receipt || end == f.sent {
		return true
	}
	for _, p := range f.pending {
		if p.record.Offset+uint64(len(p.record.Payload)) == end {
			return true
		}
	}
	return false
}
func (s *Session) validPiggybackLocked(f *stream, ack, credit uint64) bool {
	if !s.validReceiptLocked(f, ack) || credit > maxStreamID {
		return false
	}
	return credit <= f.peerCredit || credit >= f.sent && credit-f.sent <= s.limits.StreamBytes
}
func (s *Session) applyPiggybackLocked(f *stream, ack, credit uint64) {
	_ = s.receiptLocked(f, ack)
	if credit > f.peerCredit {
		f.peerCredit = credit
		s.signalOutgoing()
	}
}
func (s *Session) receiptLocked(f *stream, end uint64) error {
	if end > f.sent {
		return ErrProtocol
	}
	if end <= f.receipt {
		return nil
	}
	// Every receipt must end at a record boundary, except a final FIN ACK which
	// is also exactly the final dispatched byte. Partial unbacked acknowledgments
	// cannot discard the remainder of a retained physical record.
	valid := end == f.sent
	for _, p := range f.pending {
		if p.record.Offset+uint64(len(p.record.Payload)) == end {
			valid = true
			break
		}
	}
	if !valid {
		return ErrProtocol
	}
	f.receipt = end
	for token, job := range s.dispatches {
		if job.uncertain && job.record.Kind == Data && job.record.Stream == f.id && job.record.Offset+uint64(len(job.record.Payload)) <= end {
			delete(s.dispatches, token)
		}
	}
	kept := f.pending[:0]
	for _, p := range f.pending {
		if p.record.Offset+uint64(len(p.record.Payload)) <= end {
			n := uint64(len(p.record.Payload))
			f.buffered -= n
			s.buffered -= n
			s.records--
		} else {
			kept = append(kept, p)
		}
	}
	for i := len(kept); i < len(f.pending); i++ {
		f.pending[i] = nil
	}
	f.pending = kept
	f.signal()
	s.signalSpace()
	return nil
}
func (s *Session) dropLocked(f *stream) {
	s.buffered -= f.buffered
	s.received -= uint64(f.rx.Len()) + f.reorderBytes
	s.reserved -= f.window
	f.tx = bytes.Buffer{}
	f.rx = bytes.Buffer{}
	s.records -= len(f.pending)
	f.pending = nil
	f.reorder = nil
	f.reorderBytes = 0
	f.buffered = 0
	f.window = 0
	f.signal()
	s.signalSpace()
}

// Receive requires authenticated scope and durable physical accounting from
// the carrier. Receipts release logical replay memory, never physical quota.
func (s *Session) Receive(r Record) error {
	if !r.valid() {
		return ErrProtocol
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if r.Generation != s.generation {
		return ErrProtocol
	}
	f := s.flows[r.Stream]
	if f == nil {
		return ErrClosed
	}
	switch r.Kind {
	case Reset:
		if !f.stopped() {
			s.dropLocked(f)
		}
		f.remoteReset = true
		f.resetAckPending = true
		s.signalOutgoing()
		return nil
	case ResetAck:
		if !f.localReset || !f.resetSent {
			return ErrProtocol
		}
		f.resetAcked = true
		f.signal()
		if f.applicationClosed {
			_ = s.retireLocked(r.Stream)
		}
		return nil
	}
	if f.stopped() {
		return ErrClosed
	}
	switch r.Kind {
	case Receipt:
		return s.receiptLocked(f, r.Offset)
	case Credit:
		if !s.validPiggybackLocked(f, r.Acknowledged, r.Offset) {
			return ErrProtocol
		}
		s.applyPiggybackLocked(f, r.Acknowledged, r.Offset)
	case FinishAck:
		if !f.finishSent || r.Offset != f.sent {
			return ErrProtocol
		}
		if err := s.receiptLocked(f, r.Offset); err != nil {
			return err
		}
		f.finishAcked = true
		f.signal()
		if f.applicationClosed {
			_ = s.retireLocked(r.Stream)
		}
	case Finish:
		if r.Offset < f.recv || r.Offset > f.receiveLimit() {
			return ErrProtocol
		}
		for offset, p := range f.reorder {
			if offset+uint64(len(p)) > r.Offset {
				return ErrProtocol
			}
		}
		if f.remoteFinish && f.final != r.Offset {
			return ErrProtocol
		}
		f.remoteFinish = true
		f.final = r.Offset
		if f.recv == f.final {
			f.finishAckPending = true
			s.signalOutgoing()
		}
		f.signal()
	case Data:
		if r.ReceiveLimit != 0 && !s.validPiggybackLocked(f, r.Acknowledged, r.ReceiveLimit) {
			return ErrProtocol
		}
		end := r.Offset + uint64(len(r.Payload))
		if end <= f.recv {
			if r.ReceiveLimit != 0 {
				s.applyPiggybackLocked(f, r.Acknowledged, r.ReceiveLimit)
			}
			f.receiptPending = true
			s.signalOutgoing()
			return nil
		}
		if r.Offset < f.recv || end > f.receiveLimit() || f.remoteFinish && end > f.final {
			return ErrProtocol
		}
		if prior, exists := f.reorder[r.Offset]; exists {
			if !bytes.Equal(prior, r.Payload) {
				return ErrProtocol
			}
			return nil
		}
		for offset, p := range f.reorder {
			if r.Offset < offset+uint64(len(p)) && end > offset {
				return ErrProtocol
			}
		}
		if uint64(len(r.Payload)) > s.limits.SessionBytes-s.received {
			return ErrCapacity
		}
		// Bound sparse metadata independently of payload bytes. A carrier may not
		// fragment an issued quantum into unbounded one-byte out-of-order records.
		if r.Offset != f.recv && len(f.reorder) >= 4096 {
			return ErrCapacity
		}
		if r.ReceiveLimit != 0 {
			s.applyPiggybackLocked(f, r.Acknowledged, r.ReceiveLimit)
		}
		f.reorder[r.Offset] = append([]byte(nil), r.Payload...)
		f.reorderBytes += uint64(len(r.Payload))
		s.received += uint64(len(r.Payload))
		old := f.recv
		for {
			p, exists := f.reorder[f.recv]
			if !exists {
				break
			}
			delete(f.reorder, f.recv)
			f.rx.Write(p)
			f.recv += uint64(len(p))
			f.reorderBytes -= uint64(len(p))
		}
		if f.recv != old {
			f.receiptPending = true
			if f.remoteFinish && f.recv == f.final {
				f.finishAckPending = true
			}
			s.signalOutgoing()
			f.signal()
		}
	default:
		return ErrProtocol
	}
	return nil
}
func (s *Session) readLocked(id uint64, b []byte) (int, error) {
	f := s.flows[id]
	if s.closed || f == nil || f.stopped() || f.applicationClosed {
		return 0, ErrClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	n, _ := f.rx.Read(b)
	if n > 0 {
		f.consumed += uint64(n)
		s.received -= uint64(n)
		f.creditPending = true
		s.signalOutgoing()
		return n, nil
	}
	if f.remoteFinish && f.recv == f.final {
		return 0, io.EOF
	}
	return 0, nil
}
func (s *Session) Read(id uint64, b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(id, b)
}
func (s *Session) GrowReceive(id, target uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if s.closed || f == nil || f.stopped() {
		return ErrClosed
	}
	if target <= f.window {
		return nil
	}
	if target > s.limits.StreamBytes || target-f.window > s.limits.SessionBytes-s.reserved || f.consumed > maxStreamID-target {
		return ErrCapacity
	}
	s.reserved += target - f.window
	f.window = target
	f.creditPending = true
	s.signalOutgoing()
	return nil
}
func (s *Session) abort(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil || s.closed {
		return ErrClosed
	}
	if !f.stopped() {
		s.dropLocked(f)
	}
	f.localReset = true
	f.signal()
	s.signalOutgoing()
	return nil
}

// Retire reclaims a completed stream only after both half-closes and the
// peer's cumulative acknowledgment, or the corresponding reset handshake.
func (s *Session) Retire(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retireLocked(id)
}
func (s *Session) retireLocked(id uint64) error {
	f := s.flows[id]
	if f == nil {
		return ErrClosed
	}
	resetDone := f.stopped() && (!f.localReset || f.resetAcked) && (!f.remoteReset || f.resetAckPublished)
	graceful := !f.stopped() && f.localFinish && f.finishAcked && f.remoteFinish && f.consumed == f.final && f.finishAckPublished
	if !resetDone && !graceful {
		return ErrCapacity
	}
	for _, d := range s.dispatches {
		if d.record.Stream == id {
			return ErrCapacity
		}
	}
	if !f.stopped() {
		s.dropLocked(f)
	}
	delete(s.flows, id)
	for i, entry := range s.order {
		if entry == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	f.signal()
	s.signalOutgoing()
	return nil
}
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for _, f := range s.flows {
		if !f.stopped() {
			s.dropLocked(f)
		}
		f.signal()
	}
	s.dispatches = make(map[uint64]*dispatchState)
	s.retries = [2][]Record{}
	s.signalOutgoing()
	s.signalSpace()
	return nil
}
