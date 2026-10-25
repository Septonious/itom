package transport

import (
	"sync"
	"time"

	"itom.local/core/secure"
)

// Preparation intents prevent many accounts from durably reserving and
// canceling grants for the same short message. They reserve neither bytes nor
// quota and dequeue/encrypt nothing: the original ticket still commits first.
// Each plane has independent demand, and a backlog retains concurrent account preparation.
func (s *Session) claimPreparation(plane Plane, class LedgerClass, maximum int, bulk bool) (func(), bool) {
	s.mu.Lock()
	if s.closed || plane > Bulk || (class != DataLedger && class != ControlLedger) {
		s.mu.Unlock()
		return nil, false
	}
	s.refillInteractiveLocked(time.Now())
	capacity, progress := 0, 0
	var dataBytes uint64
	if class == ControlLedger {
		capacity = len(s.authorityOut)
	}
	for _, r := range s.retries[plane] {
		f := s.flows[r.Stream]
		if class.accepts(r.Kind) && f != nil && (!f.stopped() || r.Kind == Reset || r.Kind == ResetAck) && (r.Kind != Data || r.Offset+uint64(len(r.Payload)) > f.receipt) {
			if class == ControlLedger && r.Kind != Open && r.Kind != Reset {
				progress++
			} else {
				capacity++
				dataBytes += uint64(len(r.Payload))
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
		} else if !f.stopped() && f.opening == openEstablished && f.tx.Len() > 0 && f.sent < f.peerCredit && len(f.pending) < 4096 && s.records < 16384 && s.shortLocked(f) == (plane == Fast) {
			ready := min(uint64(f.tx.Len()), f.peerCredit-f.sent)
			capacity += int((ready + MaxPayload - 1) / MaxPayload)
			dataBytes += ready
		}
	}
	if class == ControlLedger {
		capacity += (progress + MaxBulkRecords - 1) / MaxBulkRecords
	}
	if bulk {
		// Count both per-record framing and the container's packet limit. This
		// estimates ready containers without removing bytes before durable admission.
		overhead, _ := secure.SessionPacketSize(headerSize)
		room := uint64(maximum - bulkHeader)
		encoded := dataBytes + uint64(capacity*(bulkEntryHeader+overhead))
		capacity = max((capacity+MaxBulkRecords-1)/MaxBulkRecords, int((encoded+room-1)/room))
	}
	intentFlight := len(s.dispatches)
	for _, counts := range s.preparing {
		intentFlight += counts[DataLedger] + counts[ControlLedger]
	}
	if capacity <= s.preparing[plane][class] || intentFlight >= maxDispatches || class == DataLedger && intentFlight >= dataDispatchLimit {
		s.preparationContention++
		s.mu.Unlock()
		return nil, false
	}
	s.preparing[plane][class]++
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() { s.mu.Lock(); s.preparing[plane][class]--; s.signalOutgoing(); s.mu.Unlock() })
	}, true
}
