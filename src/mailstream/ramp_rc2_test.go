package mailstream

import (
	"sort"
	"testing"
	"time"
)

// Simulated FIFO serialization with receiver-side half-second buckets. No
// bandwidth value is injected into the controller. Measure the cold startup.
func TestRC3ColdRampEvidence(t *testing.T) {
	for _, mbps := range []int{7, 35, 150, 300} {
		start := time.Unix(100, 0)
		now, finish, lastAck := start, start, start
		w := deliveryWindow{adaptive: true}
		type packet struct {
			sent, ack time.Time
			mark      deliveryMark
		}
		var pending []packet
		var buckets [30]int
		const n = 32 << 10
		maxDelay := time.Duration(0)
		for now.Sub(start) < 15*time.Second {
			for w.flight+n <= w.allowance() {
				finish = maxTime(finish, now).Add(time.Duration(n) * 8 * time.Second / time.Duration(mbps*1000000))
				ack := finish.Add(250 * time.Millisecond)
				ack = start.Add((ack.Sub(start) + 50*time.Millisecond - 1) / (50 * time.Millisecond) * (50 * time.Millisecond))
				ack = maxTime(ack, lastAck)
				lastAck = ack
				pending = append(pending, packet{now, ack, w.sent(n, now, true)})
			}
			w.limited = true
			if len(pending) == 0 {
				t.Fatal("no flight")
			}
			sort.SliceStable(pending, func(i, j int) bool { return pending[i].ack.Before(pending[j].ack) })
			p := pending[0]
			pending = pending[1:]
			now = p.ack
			w.delivered(n, now.Sub(p.sent), now, true, p.mark)
			w.flight += 128
			w.delivered(128, 120*time.Millisecond, now, false)
			maxDelay = max(maxDelay, now.Sub(p.sent))
			if i := int(now.Sub(start) / (500 * time.Millisecond)); i < len(buckets) {
				buckets[i] += n
			}
		}
		rates := make([]int, len(buckets))
		for i, b := range buckets {
			rates[i] = int(float64(b) * 8 / 500000)
		}
		t.Logf("%d Mbps: buckets=%v peak delay=%v", mbps, rates, maxDelay)
		if maxDelay > 2*time.Second {
			t.Fatalf("slow-link delay %s", maxDelay)
		}
		if mbps == 300 {
			if rates[1] < 65 || rates[2] < 110 {
				t.Fatalf("cold ramp regressed: %v", rates[:4])
			}
			for _, rate := range rates[4:] {
				if rate < 200 {
					t.Fatalf("sustained throughput regressed: %v", rates)
				}
			}
		}
	}
}
