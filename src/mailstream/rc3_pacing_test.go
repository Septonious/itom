package mailstream

import (
	"testing"
	"time"
)

// Simulate a saturated dispatcher waking on its existing 5ms timer. The
// scheduled remainder should survive a late wake, but long idle gaps should
// not earn unbounded burst credit. No provider throughput claim is implied.
func TestRC3PacingRetainsFractionalTick(t *testing.T) {
	for _, rate := range []float64{20e6, 27e6, 33e6, 45e6} {
		var p bulkPacer
		start := time.Unix(10, 0)
		sent := 0
		for i := 0; i < 400; i++ {
			now := start.Add(time.Duration(i) * 5 * time.Millisecond)
			for p.ready(32<<10, rate, now) {
				p.admitted(32 << 10)
				sent += 32 << 10
			}
		}
		got := float64(sent) / 2
		t.Logf("%.0f B/s target: %.0f B/s scheduled", rate, got)
		if got < rate*.93 || got > rate*1.08 {
			t.Fatalf("rate %.0f got %.0f", rate, got)
		}
		now := start.Add(time.Minute)
		if !p.ready(32<<10, rate, now) {
			t.Fatal("idle burst did not resume")
		}
		if now.Sub(p.started) > 5*time.Millisecond {
			t.Fatal("unbounded idle credit")
		}
	}
}

func TestRC3ModerateControlTailDoesNotWaitForIdle(t *testing.T) {
	l := &IMAPLink{}
	l.lastBulk.Store(time.Now().UnixNano())
	a := &mailAccountState{link: l, spent: 2 << 20, reserved: 2 << 20}
	a.maintenance.initialMessages = 1023
	if !a.deferMaintenanceLocked() {
		t.Fatal("small tail preempted DATA")
	}
	a.maintenance.initialMessages = 1024
	if a.deferMaintenanceLocked() {
		t.Fatal("large tail deferred indefinitely")
	}
	if a.spent != 2<<20 || a.reserved != 2<<20 {
		t.Fatal("scheduling altered durable debt")
	}
}
