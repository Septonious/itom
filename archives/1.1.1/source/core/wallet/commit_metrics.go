package wallet

import (
	"itom.local/core/internal/commitdiag"
	"sync/atomic"
)

type commitMetrics struct {
	count, failures, queueNS, commitNS, maxNS atomic.Uint64
	timeline                                  commitdiag.Tracker
}
type CommitSnapshot struct{ Count, Failures, QueueNS, CommitNS, MaxCommitNS uint64 }
type CommitEvent = commitdiag.Event
type CommitTimeline = commitdiag.Snapshot

// Runtime update metrics exclude wallet opening, directory sync and Close.
// Neither getter waits for Wallet.mu/database I/O. All snapshots are diagnostic;
// actual original transaction completion remains the sole writer authority.
func (w *Wallet) CommitSnapshot() CommitSnapshot {
	if w == nil {
		return CommitSnapshot{}
	}
	return CommitSnapshot{w.metrics.count.Load(), w.metrics.failures.Load(), w.metrics.queueNS.Load(), w.metrics.commitNS.Load(), w.metrics.maxNS.Load()}
}
func (w *Wallet) CommitTimeline() CommitTimeline {
	if w == nil {
		return CommitTimeline{SampledAtNS: commitdiag.Now()}
	}
	return w.metrics.timeline.Snapshot()
}
