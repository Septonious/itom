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
	InteractiveIdle           time.Duration
}

func DefaultLimits() Limits {
	return Limits{Streams: 128, SessionBytes: 64 << 20, StreamBytes: 8 << 20, InteractiveBytes: 16 << 10, InteractiveIdle: time.Second}
}

type stream struct {
	tx                                           bytes.Buffer
	sent, peerCredit, recv, consumed             uint64
	rx                                           bytes.Buffer
	reorder                                      map[uint64][]byte
	reorderBytes, window                         uint64
	activity                                     time.Time
	burst                                        int
	creditPending                                bool
	localFinish, finishSent, remoteFinish, reset bool
	final                                        uint64
}

// Session owns all flows. Carriers carry records, never flow ownership. There is
// no account affinity or loss of a flow when one physical service path stalls.
// Network/disk I/O is performed outside this mutex by the carrier coordinator.
type Session struct {
	mu                         sync.Mutex
	generation                 uint64
	limits                     Limits
	flows                      map[uint64]*stream
	order                      []uint64
	cursor                     [2]int
	queued, received, reserved uint64
}

func NewSession(generation uint64, l Limits) (*Session, error) {
	if generation == 0 || l.Streams < 1 || l.Streams > 4096 || l.StreamBytes == 0 || l.SessionBytes < l.StreamBytes || l.SessionBytes > 1<<30 || l.InteractiveBytes < 1 || l.InteractiveBytes > MaxPayload || l.InteractiveIdle <= 0 {
		return nil, ErrCapacity
	}
	return &Session{generation: generation, limits: l, flows: make(map[uint64]*stream)}, nil
}

// Add is called only after authenticated, idempotent OPEN negotiation. Initial
// credit is an absolute receiver-backed byte offset, not a congestion window.
func (s *Session) Add(id, initialCredit, receiveWindow uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == 0 || len(s.flows) >= s.limits.Streams || initialCredit > s.limits.StreamBytes || receiveWindow == 0 || receiveWindow > s.limits.StreamBytes || receiveWindow > s.limits.SessionBytes-s.reserved {
		return ErrCapacity
	}
	if _, ok := s.flows[id]; ok {
		return ErrProtocol
	}
	s.flows[id] = &stream{peerCredit: initialCredit, window: receiveWindow, reorder: make(map[uint64][]byte)}
	s.order = append(s.order, id)
	s.reserved += receiveWindow
	return nil
}
func (s *Session) Queue(id uint64, b []byte, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil || f.reset || f.localFinish {
		return ErrClosed
	}
	n := uint64(len(b))
	if n > s.limits.StreamBytes-uint64(f.tx.Len()) || n > s.limits.SessionBytes-s.queued {
		return ErrCapacity
	}
	// A new request after inactivity regains short-service eligibility even on a
	// previously bulk flow. Packet fragmentation alone does not reset its burst.
	if f.tx.Len() == 0 && now.Sub(f.activity) >= s.limits.InteractiveIdle {
		f.burst = 0
	}
	f.activity = now
	f.tx.Write(b)
	s.queued += n
	return nil
}
func (s *Session) Finish(id uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil || f.reset {
		return ErrClosed
	}
	f.localFinish = true
	return nil
}

// Next drains a bounded fair quantum from the requested plane. Bulk cannot use
// Fast, and Fast cannot sit behind a bulk BODY on the same executor.
func (s *Session) Next(plane Plane, max int) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if plane > Bulk || max <= 0 {
		return Record{}, false
	}
	if max > MaxPayload {
		max = MaxPayload
	}
	n := len(s.order)
	if n == 0 {
		return Record{}, false
	}
	for j := 0; j < n; j++ {
		i := (s.cursor[plane] + j) % n
		id := s.order[i]
		f := s.flows[id]
		if f.reset {
			continue
		}
		r := Record{Generation: s.generation, Stream: id}
		if plane == Fast && f.creditPending {
			f.creditPending = false
			r.Kind = Credit
			r.Offset = f.consumed + f.window
			s.cursor[plane] = (i + 1) % n
			return r, true
		}
		if plane == Fast && f.localFinish && f.tx.Len() == 0 && !f.finishSent {
			f.finishSent = true
			r.Kind = Finish
			r.Offset = f.sent
			s.cursor[plane] = (i + 1) % n
			return r, true
		}
		if f.tx.Len() == 0 || f.sent >= f.peerCredit {
			continue
		}
		short := f.burst < s.limits.InteractiveBytes && f.tx.Len() <= s.limits.InteractiveBytes-f.burst
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
		r.Payload = append([]byte(nil), f.tx.Next(take)...)
		f.sent += uint64(take)
		s.queued -= uint64(take)
		if f.burst < s.limits.InteractiveBytes {
			f.burst += take
		}
		s.cursor[plane] = (i + 1) % n
		return r, true
	}
	return Record{}, false
}

// Receive must only be called after envelope authentication, replay checking
// and durable physical accounting. Local loopback must be filtered beforehand.
func (s *Session) Receive(r Record) error {
	if !r.valid() {
		return ErrProtocol
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Generation != s.generation {
		return ErrProtocol
	}
	f := s.flows[r.Stream]
	if f == nil || f.reset {
		return ErrClosed
	}
	switch r.Kind {
	case Credit:
		if r.Offset < f.peerCredit {
			return nil
		}
		if r.Offset < f.sent || r.Offset-f.sent > s.limits.StreamBytes {
			return ErrProtocol
		}
		f.peerCredit = r.Offset
	case Finish:
		if r.Offset < f.recv || r.Offset > f.consumed+f.window {
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
	case Reset:
		s.queued -= uint64(f.tx.Len())
		s.received -= uint64(f.rx.Len()) + f.reorderBytes
		f.tx.Reset()
		f.rx.Reset()
		f.reorder = nil
		f.reorderBytes = 0
		f.reset = true
	case Data:
		end := r.Offset + uint64(len(r.Payload))
		if end <= f.recv {
			return nil
		}
		if r.Offset < f.recv || end > f.consumed+f.window || f.remoteFinish && end > f.final {
			return ErrProtocol
		}
		if prior, ok := f.reorder[r.Offset]; ok {
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
		f.reorder[r.Offset] = append([]byte(nil), r.Payload...)
		f.reorderBytes += uint64(len(r.Payload))
		s.received += uint64(len(r.Payload))
		for {
			p, ok := f.reorder[f.recv]
			if !ok {
				break
			}
			delete(f.reorder, f.recv)
			f.rx.Write(p)
			f.recv += uint64(len(p))
			f.reorderBytes -= uint64(len(p))
		}
	}
	return nil
}

// Read advances cumulative credit only after application consumption. A zero
// return without error means not ready; a blocking net.Conn adapter subscribes
// to receive notifications rather than busy polling this method.
func (s *Session) Read(id uint64, b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil || f.reset {
		return 0, ErrClosed
	}
	n, _ := f.rx.Read(b)
	if n > 0 {
		f.consumed += uint64(n)
		s.received -= uint64(n)
		f.creditPending = true
		return n, nil
	}
	if f.remoteFinish && f.recv == f.final {
		return 0, io.EOF
	}
	return 0, nil
}

// GrowReceive atomically backs additional advertised credit with the session
// memory pool. Issued credit is never revoked in response to a latency sample.
func (s *Session) GrowReceive(id, target uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil || f.reset {
		return ErrClosed
	}
	if target <= f.window {
		return nil
	}
	if target > s.limits.StreamBytes || target-f.window > s.limits.SessionBytes-s.reserved {
		return ErrCapacity
	}
	s.reserved += target - f.window
	f.window = target
	f.creditPending = true
	return nil
}
