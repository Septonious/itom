package transport

import "itom.local/core/wire"

// queueAuthority reserves bounded logical memory, never a physical permit.
// Publishers still reserve the ORIGINAL concrete CONTROL ticket before dequeue,
// encryption, durable envelope commit and I/O. In-flight unknown work stays held.
func (s *Session) queueAuthority(class LedgerClass, message wire.Control) error {
	payload, err := encodeAuthority(class, message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	// Only undispatched cumulative status may coalesce. Dispatched immutable
	// records, grants, fences and retirements never change under a writer.
	if retirement, ok := message.(wire.Retirement); ok {
		for _, r := range s.authorityOut {
			oldClass, oldMessage, e := decodeAuthority(r.Payload)
			old, yes := oldMessage.(wire.Retirement)
			if e == nil && yes && oldClass == class && old.Key == retirement.Key && old.Ack == retirement.Ack {
				if old.Final != retirement.Final {
					return ErrProtocol
				}
				return nil
			}
		}
	}
	if status, ok := message.(wire.CleanupStatus); ok {
		for n, r := range s.authorityOut {
			oldClass, oldMessage, e := decodeAuthority(r.Payload)
			old, yes := oldMessage.(wire.CleanupStatus)
			if e != nil || !yes || oldClass != class || old.Key != status.Key {
				continue
			}
			if status.Used.Messages < old.Used.Messages || status.Used.Bytes < old.Used.Bytes || status.Registered.Messages < old.Registered.Messages || status.Registered.Bytes < old.Registered.Bytes || status.Released.Messages < old.Released.Messages || status.Released.Bytes < old.Released.Bytes || old.Closed && !status.Closed {
				return nil
			}
			if uint64(len(payload)) > s.limits.SessionBytes-(s.buffered-uint64(len(r.Payload))) {
				return ErrCapacity
			}
			s.buffered -= uint64(len(r.Payload))
			s.authorityBytes -= uint64(len(r.Payload))
			clear(r.Payload)
			r.Payload = payload
			s.authorityOut[n] = r
			s.buffered += uint64(len(payload))
			s.authorityBytes += uint64(len(payload))
			s.signalOutgoing()
			return nil
		}
	}
	if s.authorityCount >= maxAuthorityQueued || s.nextAuthority == maxStreamID || uint64(len(payload)) > s.limits.SessionBytes-s.buffered {
		return ErrCapacity
	}
	s.nextAuthority++
	s.authorityOut = append(s.authorityOut, Record{Kind: Authority, Generation: s.generation, Offset: s.nextAuthority, Payload: payload})
	s.authorityCount++
	s.authorityBytes += uint64(len(payload))
	s.buffered += uint64(len(payload))
	s.signalOutgoing()
	return nil
}

func (s *Session) authorityAdmission(class LedgerClass, admit func([]Record) error) func([]Record) error {
	s.mu.Lock()
	owners := s.authorities
	s.mu.Unlock()
	return func(records []Record) error {
		for _, r := range records {
			if r.Kind != Authority {
				continue
			}
			_, message, err := decodeAuthority(r.Payload)
			if err != nil {
				return err
			}
			owner := owners[authorityKey(message).Account]
			if class != ControlLedger || owner == nil {
				return ErrProtocol
			}
			if err := owner.validate(r.Payload, true); err != nil {
				return err
			}
		}
		if admit != nil {
			return admit(records)
		}
		return nil
	}
}
