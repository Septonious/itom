package transport

import "itom.local/core/wire"

// Only the validated authority owner selects recovery priority. Logical queue
// space is never a physical permit: publication still reserves original CONTROL
// before encryption, durable commit and I/O. Unknown work retains admission.
// Historical recovery has a small shared queued/in-flight batch and yields to
// current permissions and stream protocol. It still spends original CONTROL.
func (s *Session) queueAuthorityPriority(class LedgerClass, message wire.Control, background bool) error {
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
	if background && len(s.authorityBackground) >= maxHistoricalAuthorityQueued {
		return ErrCapacity
	}
	if s.authorityCount >= maxAuthorityQueued || s.nextAuthority == maxStreamID || uint64(len(payload)) > s.limits.SessionBytes-s.buffered {
		return ErrCapacity
	}
	s.nextAuthority++
	s.authorityOut = append(s.authorityOut, Record{Kind: Authority, Generation: s.generation, Offset: s.nextAuthority, Payload: payload})
	if background {
		if s.authorityBackground == nil {
			s.authorityBackground = map[uint64]bool{}
		}
		s.authorityBackground[s.nextAuthority] = true
	}
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
