package mailstream

import (
	"testing"
	"time"
)

func TestRC21QuietLowRateReprobe(t *testing.T) {
	now := time.Unix(100, 0)
	var m mobileFlow
	m.reset(true)
	m.probeRate = 16000
	m.goodRate = 900000
	m.progress(now, 10000, 0)
	m.observeDelay(240*time.Millisecond, 203*time.Millisecond, now.Add(2*time.Second), false)
	m.progress(now.Add(2*time.Second), 10000, 0)
	if got := m.pacing(now.Add(2 * time.Second)); got != 125000 {
		t.Fatalf("quiet collapsed rate did not recover: %v", got)
	}
	if m.goodRate != 900000 {
		t.Fatal("learned rate changed")
	}
}
func TestRC21NoBlindReprobe(t *testing.T) {
	now := time.Unix(100, 0)
	for _, tc := range []struct {
		delay  time.Duration
		flight int
		drain  bool
	}{{2 * time.Second, 0, false}, {240 * time.Millisecond, 2 * maxWire, false}, {240 * time.Millisecond, 0, true}} {
		var m mobileFlow
		m.reset(true)
		m.probeRate = 16000
		m.draining = tc.drain
		m.progress(now, 0, tc.flight)
		m.observeDelay(tc.delay, 203*time.Millisecond, now.Add(2*time.Second), tc.flight > 0)
		m.progress(now.Add(2*time.Second), 0, tc.flight)
		if m.probeRate != 16000 {
			t.Fatalf("unsafe probe: %+v", tc)
		}
	}
}

func TestRC21ActiveFloorRecovery(t *testing.T) {
	at := time.Unix(100, 0)
	var m mobileFlow
	m.reset(true)
	m.probeRate = 16000
	m.lastBulk = 1300 * time.Millisecond
	m.probeAt = at
	for i := 1; i <= 6; i++ {
		now := at.Add(time.Duration(i) * time.Second)
		m.observeDelay(300*time.Millisecond, 203*time.Millisecond, now, true)
		m.observeRate(m.pacing(now)*.9, now, true, 1400*time.Millisecond, 227*time.Millisecond)
	}
	if m.probeRate < 100000 || m.probeRate > 125000 {
		t.Fatal(m.probeRate)
	}
}
func TestRC21StaleControlCannotTriggerProbe(t *testing.T) {
	at := time.Unix(100, 0)
	var m mobileFlow
	m.reset(true)
	m.probeRate = 16000
	m.progress(at, 0, 0)
	m.observeDelay(240*time.Millisecond, 203*time.Millisecond, at.Add(time.Second), false)
	m.observeDelay(1500*time.Millisecond, 203*time.Millisecond, at.Add(2*time.Second), false)
	m.progress(at.Add(2*time.Second), 0, 0)
	if m.probeRate != 16000 {
		t.Fatal(m.probeRate)
	}
}

// Real RC18 capture: service RTT can exceed 1.5 seconds while the server
// is still delivering at its 16,000 B/s floor. Control tails alone must not
// erase a bounded receipt-backed probe. Loss of receipts still drains.
func TestRC21ServiceTailDoesNotLockFloor(t *testing.T) {
	at := time.Unix(100, 0)
	var m mobileFlow
	m.reset(true)
	m.probeRate = 16000
	m.lastBulk = 1300 * time.Millisecond
	m.probeAt = at
	m.controlDelay = 240 * time.Millisecond
	for i := 1; i <= 10; i++ {
		now := at.Add(time.Duration(i) * time.Second)
		m.progress(now, uint64(i*32000), 32000)
		m.observeDelay(1800*time.Millisecond, 203*time.Millisecond, now, true)
		m.observeRate(m.pacing(now), now, true, 1400*time.Millisecond, 227*time.Millisecond)
	}
	if m.probeRate < 64000 || m.probeRate > 125000 {
		t.Fatal("floor locked or unbounded", m.probeRate)
	}
	m.progress(at.Add(14*time.Second), 320000, 32000)
	if !m.draining {
		t.Fatal("real delivery outage did not drain")
	}
}
