//go:build itomtest

package journal

import "time"

// AddCommitDelayForTest adds serialized latency before real durable commits.
// It cannot disable fsync; this API is absent from ordinary release builds.
func (s *Store) AddCommitDelayForTest(d time.Duration) {
	if d <= 0 || d > 100*time.Millisecond {
		panic("invalid commit test delay")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beforeCommit != nil {
		panic("commit fault hook already installed")
	}
	s.beforeCommit = func() error { time.Sleep(d); return nil }
}
