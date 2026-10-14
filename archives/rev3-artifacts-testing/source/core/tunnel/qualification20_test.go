package tunnel

import (
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// Delivery is counted where an application consumes and verifies bytes. The
// fixed wall-clock interval is independent of HTTP probe completion, teardown,
// provider/MIME bytes and queued work. Late bytes never enter either rate.
type deliveryWindow struct {
	begin, tail, end           time.Time
	up, down, tailUp, tailDown atomic.Int64
}

func newDeliveryWindow(begin time.Time, duration time.Duration) *deliveryWindow {
	return &deliveryWindow{begin: begin, tail: begin.Add(duration * 2 / 3), end: begin.Add(duration)}
}
func (w *deliveryWindow) add(upload bool, n int, at time.Time) {
	if w == nil || n <= 0 || at.Before(w.begin) || !at.Before(w.end) {
		return
	}
	whole, tail := &w.down, &w.tailDown
	if upload {
		whole, tail = &w.up, &w.tailUp
	}
	whole.Add(int64(n))
	if !at.Before(w.tail) {
		tail.Add(int64(n))
	}
}
func (w *deliveryWindow) rates() (up, down, tailUp, tailDown float64) {
	whole, tail := w.end.Sub(w.begin).Seconds(), w.end.Sub(w.tail).Seconds()
	return float64(w.up.Load()) * 8 / whole / 1e6, float64(w.down.Load()) * 8 / whole / 1e6, float64(w.tailUp.Load()) * 8 / tail / 1e6, float64(w.tailDown.Load()) * 8 / tail / 1e6
}
func transport20Accepted(up, down, tailUp, tailDown float64, upload, download bool, maxRTT time.Duration, probes, probeErrors int) bool {
	if !upload && !download || maxRTT <= 0 || maxRTT > 1500*time.Millisecond || probes <= 0 || probeErrors != 0 {
		return false
	}
	valid := func(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= 5 }
	return (!upload || valid(up) && valid(tailUp)) && (!download || valid(down) && valid(tailDown))
}

func TestTransport20ExactContractIncludesBothDirectionsTailAndLateProbes(t *testing.T) {
	if !transport20Accepted(5, 5, 5, 5, true, true, 1500*time.Millisecond, 60, 0) {
		t.Fatal("inclusive boundary rejected")
	}
	for _, v := range []struct {
		up, down, tailUp, tailDown float64
		rtt                        time.Duration
		errors                     int
	}{
		{math.Nextafter(5, 0), 5, 5, 5, time.Second, 0}, {5, 5, 5, math.Nextafter(5, 0), time.Second, 0},
		{5, 5, 5, 5, 1500*time.Millisecond + time.Nanosecond, 0}, {50, 50, 50, 50, time.Second, 1},
		{math.NaN(), 5, 5, 5, time.Second, 0},
	} {
		if transport20Accepted(v.up, v.down, v.tailUp, v.tailDown, true, true, v.rtt, 60, v.errors) {
			t.Fatalf("invalid qualification accepted: %+v", v)
		}
	}
}
func TestDeliveryWindowIgnoresWarmupAndLateCompletion(t *testing.T) {
	begin := time.Unix(0, 0)
	w := newDeliveryWindow(begin, 60*time.Second)
	w.add(true, 99, begin.Add(-time.Nanosecond))
	w.add(true, 37500000, begin)
	w.add(true, 12500000, w.tail)
	w.add(true, 99, w.end)
	w.add(false, 37500000, begin)
	w.add(false, 12500000, w.tail)
	up, down, tailUp, tailDown := w.rates()
	if up != 50.0/60*8 || down != up || tailUp != 5 || tailDown != 5 {
		t.Fatal(up, down, tailUp, tailDown)
	}
}
