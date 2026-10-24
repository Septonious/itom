package transport

import "time"

// minimumPayloadFor advertises the smallest ready logical quantum BEFORE a
// physical grant is reserved. Variable-size OPEN controls and immutable retry
// records must not repeatedly reserve zero-byte bodies they cannot use.
func (s *Session) minimumPayloadFor(plane Plane, class LedgerClass) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || plane > Bulk {
		return 0, false
	}
	s.refillInteractiveLocked(time.Now())
	minimum, found := MaxPayload+1, false
	consider := func(n int) { minimum, found = min(minimum, n), true }
	for _, r := range s.retries[plane] {
		f := s.flows[r.Stream]
		if f == nil || !class.accepts(r.Kind) || f.stopped() && r.Kind != Reset && r.Kind != ResetAck || r.Kind == Data && r.Offset+uint64(len(r.Payload)) <= f.receipt {
			continue
		}
		consider(len(r.Payload))
	}
	for _, f := range s.flows {
		if class != DataLedger && plane == Fast {
			if f.resetAckPending || f.localReset && !f.resetSent || !f.stopped() && (f.receiptPending || f.creditPending || f.finishAckPending || f.localFinish && f.tx.Len() == 0 && !f.finishSent) {
				consider(0)
			}
			if !f.stopped() && f.openPending {
				consider(len(f.openFrame.Payload))
			}
		}
		if class != ControlLedger && !f.stopped() && f.opening == openEstablished && f.tx.Len() > 0 && f.sent < f.peerCredit && len(f.pending) < 4096 && s.records < 16384 && s.shortLocked(f) == (plane == Fast) {
			consider(1)
		}
	}
	return minimum, found
}
