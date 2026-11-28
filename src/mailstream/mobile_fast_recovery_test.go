package mailstream

import (
	"testing"
	"time"
)

func TestRC22CleanReceiptRecovery(t *testing.T) {
	start := time.Unix(100, 0)
	for _, healthy := range []bool{true, false} {
		m := mobileFlow{enabled: true, probeRate: 250000, lastBulk: 250 * time.Millisecond, probeAt: start}
		for i := 1; i <= 6; i++ {
			now := start.Add(time.Duration(i) * 500 * time.Millisecond)
			if healthy {
				m.observeDelay(100*time.Millisecond, 90*time.Millisecond, now, true)
			}
			m.observeRate(m.pacing(now)*.98, now, true, 250*time.Millisecond, 200*time.Millisecond)
		}
		if healthy && m.probeRate < 1_500_000 {
			t.Fatalf("healthy rate did not recover: %f", m.probeRate)
		}
		// The pre-existing one-second probe produces 1,029,193.452 B/s.
		if !healthy && (m.probeRate < 1029192 || m.probeRate > 1029195) {
			t.Fatalf("unverified acceleration: %f", m.probeRate)
		}
	}
}
