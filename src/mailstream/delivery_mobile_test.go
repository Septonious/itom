package mailstream

import (
	"sort"
	"strconv"
	"testing"
	"time"
)

// Small reverse traffic measures the best-case mailbox round trip, while DATA
// arrives through a slower, batched FETCH path. Serialization and callback
// delay are independent: a larger RTT alone does not imply a slow access link.
// Respect real flight limits and use production packet delivery marks.
func TestMobileBulkEscapesSmallTrafficBaseline(t *testing.T) {
	for _, variant := range []struct {
		capacity int
		mixed    bool
	}{{7, false}, {80, false}, {80, true}} {
		capacity, mixed := variant.capacity, variant.mixed
		name := strconv.Itoa(capacity) + "Mbps"
		if mixed {
			name += "_mixed_callbacks"
		}
		t.Run(name, func(t *testing.T) {
			type packet struct {
				sent, ack time.Time
				mark      deliveryMark
			}
			const quantum = 32 << 10
			now := time.Unix(100, 0)
			w := deliveryWindow{adaptive: true}
			for run := range 2 {
				// Authenticate a small bulk response, like the TLS/HTTP setup
				// immediately preceding a speedtest's full-size bodies.
				mark := w.sent(1024, now, true)
				now = now.Add(350 * time.Millisecond)
				w.delivered(1024, 350*time.Millisecond, now, true, mark)
				start, finish := now, now
				var pending []packet
				tailBytes, peak, ordinal := 0, 0, 0
				maxDelay := time.Duration(0)
				for now.Sub(start) < 20*time.Second {
					for w.flight+quantum <= w.allowance() {
						finish = maxTime(finish, now).Add(time.Duration(quantum) * 8 * time.Second / time.Duration(capacity*1_000_000))
						delay := 700 * time.Millisecond
						if mixed {
							delay = 1100 * time.Millisecond
							if ordinal%8 == 0 {
								delay = 250 * time.Millisecond
							}
						}
						ordinal++
						arrival := finish.Add(delay)
						ack := start.Add((arrival.Sub(start) + 100*time.Millisecond - 1) / (100 * time.Millisecond) * (100 * time.Millisecond))
						pending = append(pending, packet{now, ack, w.sent(quantum, now, true)})
					}
					sort.SliceStable(pending, func(i, j int) bool { return pending[i].ack.Before(pending[j].ack) })
					w.limited = true
					if len(pending) == 0 {
						t.Fatal("no in-flight data despite queued body")
					}
					p := pending[0]
					pending = pending[1:]
					now = p.ack
					w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
					w.flight += 128
					w.delivered(128, 100*time.Millisecond, now, false)
					peak = max(peak, w.allowance())
					maxDelay = max(maxDelay, now.Sub(p.sent))
					if now.Sub(start) > 15*time.Second {
						tailBytes += quantum
					}
				}
				mbps := float64(tailBytes) * 8 / 5e6
				if mbps < float64(capacity)*.85 {
					t.Errorf("run %d mobile bulk stuck below capacity: %.1f/%d Mbps, window=%d", run+1, mbps, capacity, w.allowance())
				}
				if maxDelay > 2*time.Second || capacity == 7 && peak > 2<<20 {
					t.Errorf("run %d excessive queued mobile data: peak=%d delay=%s", run+1, peak, maxDelay)
				}
				t.Logf("run %d tail=%.1f Mbps window=%d peak=%d max RTT=%s", run+1, mbps, w.allowance(), peak, maxDelay)
				for _, p := range pending {
					now = maxTime(now, p.ack)
					w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
					w.flight += 128
					w.delivered(128, 100*time.Millisecond, now, false)
				}
				now = now.Add(5 * time.Second)
			}
		})
	}
}
