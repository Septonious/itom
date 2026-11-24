package journal

import (
	bolt "go.etcd.io/bbolt"
	"sync"
)

// PinLocalPublication is a volatile lifetime exclusion, not quota authority.
// It requires the original persisted local attempt. The trusted publisher pins
// before issuing any APPEND bytes and releases only after its bounded command
// outcome and original journal completion have drained. A fully observed BODY
// and peer receipt can precede that tag; neither may race the completion row's
// deletion. Reopening has no live old publishers and preserves all durable debt.
func (s *Store) PinLocalPublication(ref AttemptRef) (func(), error) {
	err := s.read(func(tx *bolt.Tx) error {
		a, g, err := s.attempt(tx, ref)
		if err != nil {
			return err
		}
		if g.Remote || a.State == Prepared || a.Run != s.run || s.localPublications[ref] {
			return ErrState
		}
		if s.localPublications == nil {
			s.localPublications = make(map[AttemptRef]bool)
		}
		s.localPublications[ref] = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); delete(s.localPublications, ref); s.mu.Unlock() }) }, nil
}

func (s *Store) LocalPublicationActive(ref AttemptRef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localPublications[ref]
}
