package transport

import "itom.local/core/secure"

const maxAuthorityBatchRecords = 8

const MaxControlBytes = 8 << 10

// TakeControls drains only work already ready after exact CONTROL admission.
// There is no fill timer. A single record keeps its original wire size, so a
// grant fitting one receipt/OPEN is not blocked by optional container overhead.
// Authority batches are homogeneous and bounded independently.
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
	if first.Record.Kind == Authority {
		background := s.authorityBackground[first.Record.Offset]
		for len(batch.dispatches) < maxAuthorityBatchRecords {
			remaining := maxEncryptedBytes - bulkHeader - (len(batch.dispatches)+1)*bulkEntryHeader - packetBytes - overhead
			if remaining < 0 {
				break
			}
			next, ok := s.takeAuthorityContinuationLocked(min(MaxPayload, remaining), background)
			if !ok {
				break
			}
			batch.dispatches = append(batch.dispatches, next)
			packetBytes += overhead + len(next.Record.Payload)
			batch.bytes = bulkHeader + len(batch.dispatches)*bulkEntryHeader + packetBytes
		}
		return batch, true
	}
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

// Only homogeneous authority work can extend this immutable physical batch.
// Priority cannot pull an OPEN/receipt into a deferred grant-recovery body.
func (s *Session) takeAuthorityContinuationLocked(max int, background bool) (*Dispatch, bool) {
	if len(s.dispatches) >= maxDispatches || s.nextToken == ^uint64(0) {
		return nil, false
	}
	for n, r := range s.authorityOut {
		if s.authorityBackground[r.Offset] != background || len(r.Payload) > max {
			continue
		}
		copy(s.authorityOut[n:], s.authorityOut[n+1:])
		s.authorityOut[len(s.authorityOut)-1] = Record{}
		s.authorityOut = s.authorityOut[:len(s.authorityOut)-1]
		s.nextToken++
		s.dispatches[s.nextToken] = &dispatchState{record: r, plane: Fast}
		detached := r
		detached.Payload = append([]byte(nil), r.Payload...)
		return &Dispatch{session: s, token: s.nextToken, Record: detached}, true
	}
	return nil, false
}
