package mailstream

import (
	"testing"
	"time"
)

// A constrained 35 Mbps path has a short propagation baseline and variable
// callback delay. Growing queues must not become permission for a larger flight.
// Account for MIME expansion on the serialized wire, independently of the
// authenticated payload-byte estimator, and preserve FIFO callback ordering.
func TestQueueGrowthDoesNotRaiseBulkProbeBudget(t *testing.T) {
	type packet struct {
		sent, ack time.Time
		mark      deliveryMark
	}
	start := time.Unix(100, 0)
	now, finish, lastACK := start, start, start
	w := deliveryWindow{adaptive: true}
	const quantum = 32 << 10
	var pending []packet
	peakDelay := time.Duration(0)
	tail := 0
	for now.Sub(start) < 30*time.Second {
		for w.flight+quantum <= w.allowance() {
			finish = maxTime(finish, now).Add(time.Duration(quantum) * 137 / 100 * 8 * time.Second / 35_000_000)
			// Callback service changes without changing link capacity.
			delays := []time.Duration{75, 200, 100, 250}
			delay := delays[int(finish.Sub(start)/(500*time.Millisecond))%len(delays)] * time.Millisecond
			arrival := finish.Add(delay)
			ack := start.Add((arrival.Sub(start) + 100*time.Millisecond - 1) / (100 * time.Millisecond) * (100 * time.Millisecond))
			ack = maxTime(ack, lastACK)
			lastACK = ack
			pending = append(pending, packet{now, ack, w.sent(quantum, now, true)})
		}
		w.limited = true
		if len(pending) == 0 {
			t.Fatal("stalled without a queued packet")
		}
		p := pending[0]
		pending = pending[1:]
		now = p.ack
		w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
		if now.Sub(start) > 5*time.Second {
			peakDelay = max(peakDelay, now.Sub(p.sent))
		}
		if now.Sub(start) > 25*time.Second {
			tail += quantum
		}
	}
	mbps := float64(tail) * 8 / 5e6
	if peakDelay > 2*time.Second || mbps < 22 {
		t.Fatalf("queue-derived probe growth: delay=%s tail=%.1f Mbps window=%d", peakDelay, mbps, w.allowance())
	}
	t.Logf("tail=%.1f Mbps maximum delivery RTT=%s window=%d", mbps, peakDelay, w.allowance())
}
