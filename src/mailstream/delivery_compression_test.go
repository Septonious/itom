package mailstream

import (
	"testing"
	"time"
)

// A 7 Mbps FIFO link delivers receipts in 200 ms bursts. Receipt arrival
// compression must not inflate the allowance into seconds of queued payload.
func TestCompressedBulkReceiptsKeepSlowLinkFlightBounded(t *testing.T) {
	type packet struct {
		sent, ack time.Time
		mark      deliveryMark
	}
	start := time.Unix(100, 0)
	now := start
	finish := start
	w := deliveryWindow{adaptive: true}
	var pending []packet
	const quantum = 32 << 10
	service := time.Duration(quantum) * 8 * time.Second / 7_000_000
	peak := 0
	maxDelay := time.Duration(0)
	for now.Sub(start) < 30*time.Second {
		for w.flight+quantum <= w.allowance() {
			finish = maxTime(finish, now).Add(service)
			arrival := finish.Add(75 * time.Millisecond)
			offset := arrival.Sub(start)
			ack := start.Add((offset + 200*time.Millisecond - 1) / (200 * time.Millisecond) * (200 * time.Millisecond))
			pending = append(pending, packet{now, ack, w.sent(quantum, now, true)})
		}
		w.limited = true
		p := pending[0]
		pending = pending[1:]
		now = p.ack
		w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
		if now.Sub(start) > 5*time.Second {
			peak = max(peak, w.allowance())
			maxDelay = max(maxDelay, now.Sub(p.sent))
		}
	}
	if peak > 2<<20 || maxDelay > 2*time.Second {
		t.Fatalf("compressed receipts inflated a slow link: window=%d delay=%s", peak, maxDelay)
	}
	t.Logf("peak allowance=%d maximum loaded delivery=%s", peak, maxDelay)
}
