package transport

// progressControlAdmission captures existing stream identities before AEAD.
// Its returned validator only reads that immutable snapshot: no session lock,
// DNS, disk, I/O or application mutation occurs under the crypto receive lock.
// OPEN needs ordinary admission. RESET of an existing/retired-since-snapshot
// stream frees/discards capacity; a new early RESET may need a tombstone slot.
// Other protocol records update existing state or discard a closed stream.
func (s *Session) progressControlAdmission() func([]Record) error {
	s.mu.Lock()
	existing := make(map[uint64]bool, len(s.flows))
	for id := range s.flows {
		existing[id] = true
	}
	s.mu.Unlock()
	return func(records []Record) error {
		for _, r := range records {
			if r.Kind == Data {
				return ErrProtocol
			}
			if r.Kind == Open {
				return &inboxDeferred{class: ControlLedger}
			}
			if r.Kind == Reset && !existing[r.Stream] {
				return &inboxDeferred{class: ControlLedger, transient: true}
			}
		}
		return nil
	}
}

func peerInboxAdmission(s *Session, class LedgerClass, available inboxAdmission) (func([]Record) error, bool, error) {
	if available.allows(class) {
		return nil, false, nil
	}
	if class == ControlLedger && available.progress {
		return s.progressControlAdmission(), true, nil
	}
	return nil, false, &inboxDeferred{class: class}
}
