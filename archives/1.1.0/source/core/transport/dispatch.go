package transport

import "time"

// PublicationOutcome deliberately separates a command which never started
// from provider acceptance and an uncertain physical publication. Only the
// first is eligible for automatic dispatch on another carrier.
type PublicationOutcome uint8

const (
	NotStarted PublicationOutcome = iota
	Accepted
	Uncertain
)
const maxDispatches = 256
const dataDispatchLimit = maxDispatches - 32

type dispatchState struct {
	record    Record
	plane     Plane
	uncertain bool
}
type Dispatch struct {
	session *Session
	token   uint64
	Record  Record
}

func (s *Session) Take(plane Plane, max int) (*Dispatch, bool) {
	return s.TakeFor(plane, max, AnyLedger)
}
func (s *Session) TakeFor(plane Plane, max int, class LedgerClass) (*Dispatch, bool) {
	if class > ControlLedger {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeForLocked(plane, max, class)
}
func (s *Session) takeForLocked(plane Plane, max int, class LedgerClass) (*Dispatch, bool) {
	if len(s.dispatches) >= maxDispatches || s.nextToken == ^uint64(0) {
		return nil, false
	}
	if len(s.dispatches) >= dataDispatchLimit {
		if plane == Bulk || class == DataLedger {
			return nil, false
		}
		if class == AnyLedger {
			class = ControlLedger
		}
	}
	s.refillInteractiveLocked(time.Now())
	r, ok := s.nextLocked(plane, max, class)
	if !ok {
		return nil, false
	}
	s.nextToken++
	s.dispatches[s.nextToken] = &dispatchState{record: r, plane: plane}
	copy := r
	copy.Payload = append([]byte(nil), r.Payload...)
	return &Dispatch{session: s, token: s.nextToken, Record: copy}, true
}

// Complete must be called even if the originating application closed. An
// Uncertain result remains retained for explicit peer/status recovery; it is
// never automatically re-APPENDed or treated as a quota refund.
func (d *Dispatch) Complete(outcome PublicationOutcome) error { return d.complete(outcome, false) }
func (d *Dispatch) complete(outcome PublicationOutcome, proofOfStorage bool) error {
	if d == nil || d.session == nil || outcome > Uncertain {
		return ErrProtocol
	}
	s := d.session
	s.mu.Lock()
	defer s.mu.Unlock()
	return d.completeLocked(outcome, proofOfStorage)
}
func (d *Dispatch) completeLocked(outcome PublicationOutcome, proofOfStorage bool) error {
	s := d.session
	state, exists := s.dispatches[d.token]
	if !exists {
		return ErrClosed
	}
	if state.uncertain && (!proofOfStorage || outcome != Accepted) {
		return ErrProtocol
	}
	if outcome == Uncertain {
		state.uncertain = true
	} else {
		delete(s.dispatches, d.token)
	}
	f := s.flows[state.record.Stream]
	if f == nil || s.closed {
		return ErrClosed
	}
	if outcome == NotStarted {
		// Keep a bounded exact immutable record. Requeuing does not allocate a new
		// logical offset, nonce, ticket or physical descriptor.
		s.queueUnstartedLocked(state)
	} else if state.record.Kind == Data {
		for _, chunk := range f.pending {
			if chunk.record.Offset == state.record.Offset {
				chunk.outcome = outcome
				chunk.returned = true
				break
			}
		}
	} else if outcome == Accepted {
		if state.record.Kind == FinishAck {
			f.finishAckPublished = true
		}
		if state.record.Kind == ResetAck {
			f.resetAckPublished = true
		}
	}
	s.signalOutgoing()
	f.signal()
	if f.applicationClosed {
		_ = s.retireLocked(state.record.Stream)
	}
	return nil
}

// PendingPublication reports uncertain logical DATA for a status/recovery
// worker. It conveys no permission to retry or refund the physical message.
func (s *Session) PendingPublication(id uint64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[id]
	if f == nil {
		return 0
	}
	n := 0
	for _, chunk := range f.pending {
		if chunk.returned && chunk.outcome == Uncertain {
			n++
		}
	}
	return n
}
