package mailstream

import (
	"testing"
	"time"
)

// Independent FIFO serialization and batched feedback, rather than feeding a
// guessed Mbps value into the controller. Each 8-second transfer is followed by
// a quiet phase with small reverse probes, as in repeated mobile speedtests.
func TestShortRepeatedTransfersReachCapacityBeforeTheyEnd(t *testing.T) {
	type packet struct {
		sent, ack time.Time
		mark      deliveryMark
	}
	const quantum = 32 << 10
	now := time.Unix(100, 0)
	w := deliveryWindow{adaptive: true}
	for run := range 3 {
		start, finish, lastACK := now, now, now
		var pending []packet
		var buckets [16]int
		for now.Sub(start) < 8*time.Second {
			for w.flight+quantum <= w.allowance() {
				finish = maxTime(finish, now).Add(time.Duration(quantum) * 8 * time.Second / 150_000_000)
				arrival := finish.Add(250 * time.Millisecond)
				// Provider callbacks batch receipts at 50 ms boundaries.
				ack := start.Add((arrival.Sub(start) + 50*time.Millisecond - 1) / (50 * time.Millisecond) * (50 * time.Millisecond))
				ack = maxTime(ack, lastACK)
				lastACK = ack
				pending = append(pending, packet{now, ack, w.sent(quantum, now, true)})
			}
			w.limited = true
			if len(pending) == 0 {
				t.Fatal("sender deadlocked without in-flight data")
			}
			p := pending[0]
			pending = pending[1:]
			now = p.ack
			w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
			if i := int(now.Sub(start) / (500 * time.Millisecond)); i < len(buckets) {
				buckets[i] += quantum
			}
		}
		for _, p := range pending {
			now = maxTime(now, p.ack)
			w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
		}
		var total int
		ramped := false
		for i, n := range buckets {
			total += n
			if i < 4 && float64(n)*8/500_000 >= 100 {
				ramped = true
			}
		}
		avg := float64(total) * 8 / 8e6
		if !ramped || avg < 100 {
			t.Errorf("run %d ramp=%v eight-second average=%.1f Mbps half-second bytes=%v", run+1, ramped, avg, buckets)
		}
		for range 6 {
			now = now.Add(2 * time.Second)
			w.flight += 256
			w.delivered(256, 5*time.Millisecond, now, false)
		}
	}
}
