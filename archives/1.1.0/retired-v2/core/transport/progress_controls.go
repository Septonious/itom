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
			if r.Kind == Authority && !authorityProgress(r.Payload) {
				return &inboxDeferred{class: ControlLedger, kind: r.Kind}
			}
			if r.Kind == Open {
				return &inboxDeferred{class: ControlLedger, kind: r.Kind}
			}
			if r.Kind == Reset && !existing[r.Stream] {
				return &inboxDeferred{class: ControlLedger, transient: true}
			}
		}
		return nil
	}
}

// This runs against an immutable authority routing view, including inside the
// crypto validator. No mutable owner lock or wallet/journal I/O occurs here.
func historicalAuthorityGroup(records []Record, owners map[string]*AuthorityController) bool {
	if len(records) == 0 {
		return false
	}
	for _, r := range records {
		if r.Kind != Authority {
			return false
		}
		_, message, err := decodeAuthority(r.Payload)
		if err != nil {
			return false
		}
		k := authorityKey(message)
		owner := owners[k.Account]
		if owner == nil || k.Session == owner.sessionID {
			return false
		}
	}
	return true
}
func allAuthorityProgress(records []Record) bool {
	for _, r := range records {
		if r.Kind != Authority || !authorityProgress(r.Payload) {
			return false
		}
	}
	return len(records) > 0
}

func peerInboxAdmission(s *Session, class LedgerClass, available inboxAdmission) (func([]Record) error, bool, error) {
	if class != ControlLedger {
		if available.allows(class) {
			return nil, false, nil
		}
		return nil, false, &inboxDeferred{class: class}
	}
	s.mu.Lock()
	owners := s.authorities
	s.mu.Unlock()
	progress := s.progressControlAdmission()
	return func(records []Record) error {
		if historicalAuthorityGroup(records, owners) {
			if available.recovery || available.progress && allAuthorityProgress(records) {
				return nil
			}
			return &inboxDeferred{class: ControlLedger, background: true, kind: Authority}
		}
		if available.control {
			return nil
		}
		if available.progress {
			return progress(records)
		}
		kind := Kind(0)
		if len(records) > 0 {
			kind = records[0].Kind
		}
		return &inboxDeferred{class: ControlLedger, kind: kind}
	}, !available.control && available.progress, nil
}
