package mailstream

import (
	"testing"
	"time"
)

func TestControlProviderTailNeedsIndependentBulkQueueEvidence(t *testing.T) {
	now := time.Unix(100, 0)
	w := deliveryWindow{adaptive: true, limit: 8 << 20, rate: 10 << 20,
		base: 350 * time.Millisecond, saturatedBase: 500 * time.Millisecond,
		bulkRTT: 600 * time.Millisecond, controlBase: 100 * time.Millisecond}
	w.observeInteractive(900*time.Millisecond, now)
	if w.pacer.guarded || w.allowance() >= 8<<20 || !now.Before(w.controlSlowUntil) {
		t.Fatal("provider-only control tail armed persistent pacing or skipped flight protection")
	}
	w.bulkRTT = time.Second
	w.observeInteractive(900*time.Millisecond, now.Add(time.Second))
	if !w.pacer.guarded {
		t.Fatal("shared control and DATA queue evidence did not activate pacing")
	}
	w.pacer.guarded = false
	w.bulkRTT = 0
	w.observeInteractive(2*time.Second, now.Add(2*time.Second))
	if !w.pacer.guarded {
		t.Fatal("severe control delay waited for a bulk delay sample")
	}
}
