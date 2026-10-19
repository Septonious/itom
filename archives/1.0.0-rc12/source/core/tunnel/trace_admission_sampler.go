package tunnel

import (
	"sync/atomic"
	"time"
)

// admissionTraceSampler bounds diagnostic write amplification without changing
// the admission decision, job retry, wakeups, or delivery accounting.
// A sample contains the cumulative and since-last-sample deferred counts.
type admissionTraceSampler struct {
	total    atomic.Uint64
	reported atomic.Uint64
	lastNano atomic.Int64
}

const deferredAdmissionTraceInterval = 250 * time.Millisecond

func (s *admissionTraceSampler) observe(now time.Time) (emit bool, total, sinceLast uint64) {
	total = s.total.Add(1)
	nanos := now.UnixNano()
	for {
		last := s.lastNano.Load()
		if last != 0 && nanos >= last && nanos-last < int64(deferredAdmissionTraceInterval) {
			return false, total, 0
		}
		// A backward wall-clock adjustment must not create unlimited logging.
		if last != 0 && nanos < last {
			return false, total, 0
		}
		if s.lastNano.CompareAndSwap(last, nanos) {
			old := s.reported.Swap(total)
			return true, total, total - old
		}
	}
}
