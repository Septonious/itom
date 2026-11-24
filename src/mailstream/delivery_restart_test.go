package mailstream

import (
	"testing"
	"time"
)

// A speedtest sends an upload, spends thirty seconds downloading with only
// isolated reverse probes, then starts a second upload. A faster probe path
// must not permanently cap the next bulk burst at 512 KiB / bulk RTT.
func TestBulkBandwidthRestartsAfterFastLightProbes(t *testing.T) {
	var w deliveryWindow
	now := time.Unix(100, 0)
	acknowledge := func(n int, delay time.Duration, bulk bool) { w.flight += n; w.delivered(n, delay, now, bulk) }
	for range 80 {
		now = now.Add(25 * time.Millisecond)
		acknowledge(256<<10, 100*time.Millisecond, true)
	}
	for range 15 {
		now = now.Add(2 * time.Second)
		acknowledge(1<<10, time.Millisecond, false)
	}
	// A larger ordinary reverse probe must also be treated as an isolated
	// burst, not averaged across two seconds of application-limited silence.
	now = now.Add(2 * time.Second)
	acknowledge(16<<10, 5*time.Millisecond, true)
	now = now.Add(2 * time.Second)
	const quantum = 32 << 10
	delivered := 0
	// Respect actual flight allowance and a 100 ms bulk delivery RTT. Ten
	// acknowledgement rounds are one second of a fresh saturated transfer.
	for range 10 {
		window := w.allowance()
		packets := min(window, 4<<20) / quantum
		now = now.Add(100 * time.Millisecond)
		for range packets {
			acknowledge(quantum, 100*time.Millisecond, true)
			delivered += quantum
		}
	}
	mbps := float64(delivered) * 8 / 1e6
	if mbps < 100 {
		t.Fatalf("fresh bulk remained probe/window limited: %.1f Mbps window=%d", mbps, w.allowance())
	}
	t.Logf("second bulk burst delivered %.1f Mbps in its first second after light probes", mbps)
}
