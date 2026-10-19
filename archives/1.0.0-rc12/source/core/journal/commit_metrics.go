package journal

import "sync/atomic"

type commitMetrics struct{ count, failures, queueNS, commitNS, maxNS, noop atomic.Uint64 }
type CommitSnapshot struct{ Count, Failures, QueueNS, CommitNS, MaxCommitNS, Noop uint64 }

// No database lock or IO: safe to call while reporting carrier health.
func (s *Store) CommitSnapshot() CommitSnapshot {
	if s == nil {
		return CommitSnapshot{}
	}
	return CommitSnapshot{Count: s.metrics.count.Load(), Failures: s.metrics.failures.Load(), QueueNS: s.metrics.queueNS.Load(), CommitNS: s.metrics.commitNS.Load(), MaxCommitNS: s.metrics.maxNS.Load(), Noop: s.metrics.noop.Load()}
}
