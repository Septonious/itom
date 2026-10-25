package journal

import "itom.local/core/internal/commitdiag"

const maxCommitEvents = commitdiag.MaxEvents

type CommitEvent = commitdiag.Event
type CommitTimeline = commitdiag.Snapshot
type commitTimeline = commitdiag.Tracker

// No journal/database lock or I/O. Wallet and journal observations share one
// process-local monotonic origin; clocks from different machines cannot be
// subtracted. Events describe complete updates, not isolated fsync or path RTT.
func (s *Store) CommitTimeline() CommitTimeline {
	if s == nil {
		return CommitTimeline{SampledAtNS: commitdiag.Now()}
	}
	return s.metrics.timeline.Snapshot()
}
