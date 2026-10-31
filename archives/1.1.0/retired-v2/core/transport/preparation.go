package transport

import (
	"sync"
	"time"
)

// Fast preparation intents prevent many accounts from durably reserving and
// canceling grants for the same short message. They reserve neither bytes nor
// quota and dequeue/encrypt nothing: the original ticket still commits first.
// Bulk retains independent preparation, including when each account can fit
// only part of a ready burst into its original remaining allowance.
func (s *Session) claimFastPreparation(class LedgerClass) (func(), bool) {
	s.mu.Lock()
	if s.closed || (class != DataLedger && class != ControlLedger) {
		s.mu.Unlock()
		return nil, false
	}
	s.refillInteractiveLocked(time.Now())
	capacity, progress := 0, 0
	if class == ControlLedger {
		capacity = len(s.authorityOut)
	}
	for _, r := range s.retries[Fast] {
		f := s.flows[r.Stream]
		if class.accepts(r.Kind) && f != nil && (!f.stopped() || r.Kind == Reset || r.Kind == ResetAck) && (r.Kind != Data || r.Offset+uint64(len(r.Payload)) > f.receipt) {
			if class == ControlLedger && r.Kind != Open && r.Kind != Reset {
				progress++
			} else {
				capacity++
			}
		}
	}
	for _, f := range s.flows {
		if class == ControlLedger {
			if f.resetAckPending {
				progress++
			}
			if f.localReset && !f.resetSent {
				capacity++
			}
			if !f.stopped() {
				if f.openPending {
					if f.openFrame.Kind == Open {
						capacity++
					} else {
						progress++
					}
				}
				if f.finishAckPending || f.creditPending || f.receiptPending {
					progress++
				}
				if f.localFinish && f.tx.Len() == 0 && !f.finishSent {
					progress++
				}
			}
		} else if !f.stopped() && f.opening == openEstablished && f.tx.Len() > 0 && f.sent < f.peerCredit && len(f.pending) < 4096 && s.records < 16384 && s.shortLocked(f) {
			ready := min(uint64(f.tx.Len()), f.peerCredit-f.sent)
			capacity += int((ready + MaxPayload - 1) / MaxPayload)
		}
	}
	if class == ControlLedger {
		capacity += (progress + MaxBulkRecords - 1) / MaxBulkRecords
	}
	intentFlight := len(s.dispatches) + s.preparingFast[DataLedger] + s.preparingFast[ControlLedger]
	if capacity <= s.preparingFast[class] || intentFlight >= maxDispatches || class == DataLedger && intentFlight >= dataDispatchLimit {
		s.preparationContention++
		s.mu.Unlock()
		return nil, false
	}
	s.preparingFast[class]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() { s.mu.Lock(); s.preparingFast[class]--; s.signalOutgoing(); s.mu.Unlock() })
	}, true
}
