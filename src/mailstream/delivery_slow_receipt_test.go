package mailstream

import (
	"testing"
	"time"
)

// Independent FIFO-link simulation: 100 Mbps serialization plus 500 ms receipt
// latency. Fast interactive probes coexist with a continuously queued upload.
// It must grow past the initial flight instead of reinforcing its own low rate.
func TestSaturatedUploadWithSlowBulkReceipts(t *testing.T) {
	type packet struct {
		sent, ack time.Time
		n         int
	}
	start := time.Unix(100, 0)
	now := start
	finish := start
	w := deliveryWindow{adaptive: true}
	var packets []packet
	const quantum = 32 << 10
	service := time.Duration(float64(quantum) * 8 / 100_000_000 * float64(time.Second))
	var tailBytes int
	for now.Sub(start) < 15*time.Second {
		for w.flight+quantum <= w.allowance() {
			finish = maxTime(finish, now).Add(service)
			packets = append(packets, packet{now, finish.Add(500 * time.Millisecond), quantum})
			w.flight += quantum
		}
		w.limited = true
		p := packets[0]
		packets = packets[1:]
		now = p.ack
		w.delivered(p.n, now.Sub(p.sent), now, true)
		if now.Sub(start) > 11*time.Second {
			tailBytes += p.n
		}
		w.flight += 256
		w.delivered(256, 5*time.Millisecond, now, false)
	}
	mbps := float64(tailBytes) * 8 / 4e6
	if mbps < 90 {
		t.Fatalf("bulk stayed self-limited despite a capable link: final four seconds %.1f Mbps, allowance=%d", mbps, w.allowance())
	}
	t.Logf("final four-second delivery %.1f Mbps, allowance=%d", mbps, w.allowance())
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
