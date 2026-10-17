package journal

import "sync/atomic"

type commitMetrics struct{ count, failures, queueNS, commitNS, maxNS atomic.Uint64 }
type CommitSnapshot struct{ Count, Failures, QueueNS, CommitNS, MaxCommitNS uint64 }

// No database lock or IO: safe to call while reporting carrier health.
func (s *Store) CommitSnapshot() CommitSnapshot {
	if s == nil {
		return CommitSnapshot{}
	}
	return CommitSnapshot{s.metrics.count.Load(), s.metrics.failures.Load(), s.metrics.queueNS.Load(), s.metrics.commitNS.Load(), s.metrics.maxNS.Load()}
}
