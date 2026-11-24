// Package commitdiag observes serialized durable updates. It holds no database
// lock, carries no authority, and retains no payload or operation arguments.
package commitdiag

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const MaxEvents = 8

var clock = time.Now()

func Now() int64 { return time.Since(clock).Nanoseconds() }

type Event struct {
	Sequence              uint64
	StartedNS, FinishedNS int64
	Failed                bool
}
type Snapshot struct {
	SampledAtNS, ActiveSinceNS int64
	Slowest                    []Event
}

// Begin/Finish are called under the existing owner's serialized update lock.
// Snapshot never acquires that lock and can observe a stalled durable update.
type Tracker struct {
	active  atomic.Int64
	mu      sync.Mutex
	slowest []Event
}

func (t *Tracker) Begin() int64 { start := Now(); t.active.Store(start); return start }
func (t *Tracker) Finish(sequence uint64, start int64, failed bool) {
	e := Event{sequence, start, Now(), failed}
	t.mu.Lock()
	if len(t.slowest) < MaxEvents || e.FinishedNS-e.StartedNS > t.slowest[len(t.slowest)-1].FinishedNS-t.slowest[len(t.slowest)-1].StartedNS {
		t.slowest = append(t.slowest, e)
		sort.Slice(t.slowest, func(i, j int) bool {
			return t.slowest[i].FinishedNS-t.slowest[i].StartedNS > t.slowest[j].FinishedNS-t.slowest[j].StartedNS
		})
		if len(t.slowest) > MaxEvents {
			t.slowest = t.slowest[:MaxEvents]
		}
	}
	t.mu.Unlock()
	t.active.Store(0)
}
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	slowest := append([]Event(nil), t.slowest...)
	t.mu.Unlock()
	return Snapshot{Now(), t.active.Load(), slowest}
}
