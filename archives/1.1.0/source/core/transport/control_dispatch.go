package transport

import "itom.local/core/secure"

const MaxControlBytes = 8 << 10

// TakeControls drains only work already ready after exact CONTROL admission.
// There is no fill timer. A single record keeps its original wire size, so a
// grant fitting one receipt/OPEN is not blocked by optional container overhead.
// OPEN and RESET remain single: allocation/tombstone admission must not hold
// progress-only receipts behind a full receiver protocol queue.
func (s *Session) TakeControls(maxEncryptedBytes int) (*DispatchBatch, bool) {
	maxEncryptedBytes = min(maxEncryptedBytes, MaxControlBytes)
	overhead, _ := secure.SessionPacketSize(headerSize)
	if maxEncryptedBytes < overhead {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	first, ok := s.takeForLocked(Fast, min(MaxPayload, maxEncryptedBytes-overhead), ControlLedger)
	if !ok {
		return nil, false
	}
	packetBytes := overhead + len(first.Record.Payload)
	batch := &DispatchBatch{session: s, dispatches: []*Dispatch{first}, bytes: packetBytes}
	if first.Record.Kind == Open || first.Record.Kind == Reset {
		return batch, true
	}
	for len(batch.dispatches) < MaxBulkRecords {
		remaining := maxEncryptedBytes - bulkHeader - (len(batch.dispatches)+1)*bulkEntryHeader - packetBytes - overhead
		if remaining < 0 {
			break
		}
		next, ok := s.takeFilteredLocked(Fast, min(MaxPayload, remaining), ControlLedger, true)
		if !ok {
			break
		}
		batch.dispatches = append(batch.dispatches, next)
		packetBytes += overhead + len(next.Record.Payload)
		batch.bytes = bulkHeader + len(batch.dispatches)*bulkEntryHeader + packetBytes
	}
	return batch, true
}
