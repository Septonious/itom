package tunnel

import (
	"sync"
	"time"
)

// Application consumption owns the byte clock. Sampling and probe scheduling
// cannot move bytes across the fixed deadline or manufacture tail goodput.
type applicationByteClock struct {
	begin, end time.Time
	cadence    time.Duration
	mu         sync.Mutex
	up, down   []int64
}

func newApplicationByteClock(begin time.Time, duration, cadence time.Duration) *applicationByteClock {
	n := int((duration + cadence - 1) / cadence)
	return &applicationByteClock{begin: begin, end: begin.Add(duration), cadence: cadence, up: make([]int64, n), down: make([]int64, n)}
}
func (w *applicationByteClock) add(upload bool, n int, at time.Time) {
	if n <= 0 || at.Before(w.begin) || !at.Before(w.end) {
		return
	}
	i := int(at.Sub(w.begin) / w.cadence)
	w.mu.Lock()
	defer w.mu.Unlock()
	if upload {
		w.up[i] += int64(n)
	} else {
		w.down[i] += int64(n)
	}
}
func (w *applicationByteClock) totals(through int) (up, down int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := 0; i < min(through, len(w.up)); i++ {
		up += w.up[i]
		down += w.down[i]
	}
	return
}
func (t *productionLoadTarget) recordWindow(upload bool, n int) {
	if w := t.measurement.Load(); w != nil {
		w.add(upload, n, time.Now())
	}
}
