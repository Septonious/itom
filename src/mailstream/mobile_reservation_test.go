package mailstream

import (
	"testing"
	"time"
)

func TestMobileReverseReservationDoesNotRatchetLearnedRate(t *testing.T) {
	now := time.Unix(1000, 0)
	m := mobileFlow{enabled: true, probeRate: 400000, reverseDemand: true, controlDelay: 300 * time.Millisecond, lastRate: 400000, progressAt: now}
	for i := 0; i < 3; i++ {
		at := now.Add(time.Duration(i) * 3 * time.Second)
		m.progressAt = at
		m.observeDelay(300*time.Millisecond, 100*time.Millisecond, at, true)
		if m.probeRate != 400000 || m.pacing(at) != 352000 {
			t.Fatalf("iteration %d: learned %g, effective %g", i, m.probeRate, m.pacing(at))
		}
		if m.pacing(at.Add(2*time.Second)) != 400000 {
			t.Fatal("expired reservation still cuts rate")
		}
	}
	m.reverseDemand = false
	if m.pacing(now.Add(6*time.Second)) != 400000 {
		t.Fatal("cleared demand still cuts rate")
	}
	m.reverseDemand = true
	at := now.Add(10 * time.Second)
	m.progressAt = at.Add(-2 * time.Second)
	m.observeDelay(1800*time.Millisecond, 100*time.Millisecond, at, true)
	if m.probeRate >= 400000 || m.pacing(at) > m.probeRate {
		t.Fatal("real congestion must still reduce learned rate")
	}
}
