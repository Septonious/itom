package transport

import "itom.local/core/secure"

type publicationWork interface {
	complete(PublicationOutcome, bool) error
}

// DispatchBatch is one physical single-ledger publication. Every record remains part of
// its stream's independent receipt/credit state; there is no account owner.
type DispatchBatch struct {
	session    *Session
	dispatches []*Dispatch
	bytes      int
}

// TakeBulk is called only after reserving the concrete physical grant's byte
// allowance. It drains available work immediately, without a fill timer. A
// partially filled batch is valid; it cannot contain fast or control work.
func (s *Session) TakeBulk(maxEncryptedBytes int) (*DispatchBatch, bool) {
	if maxEncryptedBytes > MaxBulkBytes {
		maxEncryptedBytes = MaxBulkBytes
	}
	overhead, _ := secure.SessionPacketSize(headerSize)
	if maxEncryptedBytes < bulkHeader+bulkEntryHeader+overhead+1 {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := &DispatchBatch{session: s, bytes: bulkHeader}
	for len(batch.dispatches) < MaxBulkRecords {
		remaining := maxEncryptedBytes - batch.bytes - bulkEntryHeader - overhead
		if remaining <= 0 {
			break
		}
		if remaining > MaxPayload {
			remaining = MaxPayload
		}
		dispatch, ok := s.takeForLocked(Bulk, remaining, DataLedger)
		if !ok {
			break
		}
		batch.dispatches = append(batch.dispatches, dispatch)
		batch.bytes += bulkEntryHeader + overhead + len(dispatch.Record.Payload)
	}
	if len(batch.dispatches) == 0 {
		return nil, false
	}
	return batch, true
}
func (b *DispatchBatch) Records() []Record {
	records := make([]Record, 0, len(b.dispatches))
	for _, d := range b.dispatches {
		r := d.Record
		r.Payload = append([]byte(nil), r.Payload...)
		records = append(records, r)
	}
	return records
}
func (b *DispatchBatch) EncryptedBytes() int                       { return b.bytes }
func (b *DispatchBatch) Complete(outcome PublicationOutcome) error { return b.complete(outcome, false) }
func (b *DispatchBatch) complete(outcome PublicationOutcome, proof bool) error {
	if b == nil || b.session == nil || len(b.dispatches) == 0 || outcome > Uncertain {
		return ErrProtocol
	}
	s := b.session
	s.mu.Lock()
	defer s.mu.Unlock()
	// Validate the whole transition before mutating any live member. Receipt
	// progress may already have resolved a member, which is safely idempotent.
	for _, d := range b.dispatches {
		if state, ok := s.dispatches[d.token]; ok && state.uncertain && (!proof || outcome != Accepted) {
			return ErrProtocol
		}
	}
	for _, d := range b.dispatches {
		if _, ok := s.dispatches[d.token]; !ok {
			continue
		}
		if err := d.completeLocked(outcome, proof); err != nil && err != ErrClosed {
			return err
		}
	}
	return nil
}
