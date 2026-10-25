package journal

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var commitClock = time.Now()

const maxCommitEvents = 8

type CommitEvent struct {
	Sequence              uint64
	StartedNS, FinishedNS int64
	Failed                bool
}
type CommitTimeline struct {
	SampledAtNS, ActiveSinceNS int64
	// Slowest retains the eight longest complete journal updates, not bodies,
	// operation inputs or an inferred fsync/network RTT measurement.
	Slowest []CommitEvent
}
type commitTimeline struct {
	active  atomic.Int64
	mu      sync.Mutex
	slowest []CommitEvent
}

func commitNow() int64 { return time.Since(commitClock).Nanoseconds() }
func (t *commitTimeline) finish(sequence uint64, start int64, failed bool) {
	e := CommitEvent{Sequence: sequence, StartedNS: start, FinishedNS: commitNow(), Failed: failed}
	t.mu.Lock()
	if len(t.slowest) < maxCommitEvents || e.FinishedNS-e.StartedNS > t.slowest[len(t.slowest)-1].FinishedNS-t.slowest[len(t.slowest)-1].StartedNS {
		t.slowest = append(t.slowest, e)
		sort.Slice(t.slowest, func(i, j int) bool {
			return t.slowest[i].FinishedNS-t.slowest[i].StartedNS > t.slowest[j].FinishedNS-t.slowest[j].StartedNS
		})
		if len(t.slowest) > maxCommitEvents {
			t.slowest = t.slowest[:maxCommitEvents]
		}
	}
	t.mu.Unlock()
	t.active.Store(0)
}

// CommitTimeline takes no Store/database lock and performs no I/O. A blocked
// durable update remains observable. Times use one process-local monotonic
// origin; another machine's timestamps cannot be subtracted as path latency.
func (s *Store) CommitTimeline() CommitTimeline {
	if s == nil {
		return CommitTimeline{SampledAtNS: commitNow()}
	}
	m := &s.metrics.timeline
	m.mu.Lock()
	recent := append([]CommitEvent(nil), m.slowest...)
	m.mu.Unlock()
	return CommitTimeline{SampledAtNS: commitNow(), ActiveSinceNS: m.active.Load(), Slowest: recent}
}
