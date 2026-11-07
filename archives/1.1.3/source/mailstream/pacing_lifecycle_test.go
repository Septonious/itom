package mailstream

import (
	"testing"
	"time"
)

func TestPacingOnlyFollowsObservedCongestionUntilBulkIdle(t *testing.T) {
	now := time.Now()
	w := deliveryWindow{adaptive: true, rate: 1 << 20, base: 200 * time.Millisecond, saturatedBase: 300 * time.Millisecond, bulkRTT: 2 * time.Second, last: now, limit: 8 << 20, controlBase: 200 * time.Millisecond}
	if w.pacingRate(now) != 0 {
		t.Fatal("slow DATA alone enabled pacing on a quiet shared path")
	}
	w.observeInteractive(900*time.Millisecond, now.Add(-100*time.Millisecond))
	w.observeInteractive(900*time.Millisecond, now)
	if w.pacingRate(now) == 0 || w.pacingRate(now.Add(5*time.Second)) == 0 {
		t.Fatal("observed congested burst stopped smoothing before bulk idle")
	}
	// Preserve the original controller's learned repeat allowance. Only the
	// pacing decision ends at the existing idle boundary.
	w.limit = 8 << 20
	w.sent(32<<10, now.Add(5*time.Second), true)
	if w.pacingRate(now.Add(5*time.Second)) != 0 || w.allowance() != 8<<20 {
		t.Fatal("new quiet burst inherited pacing or lost its learned allowance")
	}
}
