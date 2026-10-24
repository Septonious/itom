package transport

// queueUnstartedLocked preserves logical work without an arbitrary retry
// queue cap below the already enforced retained-record bound. DATA entries are
// unique retained chunks; cumulative controls coalesce per live stream/kind.
// It is never called for a possibly stored publication.
func (s *Session) queueUnstartedLocked(state *dispatchState) {
	r := state.record
	f := s.flows[r.Stream]
	if f == nil {
		return
	}
	if f.stopped() && r.Kind != Reset && r.Kind != ResetAck {
		return
	}
	if r.Kind != Data {
		for i, old := range s.retries[state.plane] {
			if old.Stream != r.Stream || old.Kind != r.Kind {
				continue
			}
			if r.Kind == Credit {
				if old.Offset > r.Offset {
					r.Offset = old.Offset
				}
				if old.Acknowledged > r.Acknowledged {
					r.Acknowledged = old.Acknowledged
				}
			}
			if r.Kind == Receipt && old.Offset > r.Offset {
				r.Offset = old.Offset
			}
			s.retries[state.plane][i] = r
			return
		}
	}
	s.retries[state.plane] = append(s.retries[state.plane], r)
}
func (s *Session) discardRetriesLocked(id uint64) {
	for plane := range s.retries {
		queue := s.retries[plane]
		kept := queue[:0]
		for _, r := range queue {
			if r.Stream != id {
				kept = append(kept, r)
			}
		}
		for i := len(kept); i < len(queue); i++ {
			queue[i] = Record{}
		}
		s.retries[plane] = kept
	}
}
