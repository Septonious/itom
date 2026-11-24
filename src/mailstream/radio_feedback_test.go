package mailstream

import (
	"container/heap"
	"testing"
	"time"
)

type radioReceipt struct {
	sent, at time.Time
	mark     deliveryMark
}
type radioReceipts []radioReceipt

func (q radioReceipts) Len() int           { return len(q) }
func (q radioReceipts) Less(i, j int) bool { return q[i].at.Before(q[j].at) }
func (q radioReceipts) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *radioReceipts) Push(x any)        { *q = append(*q, x.(radioReceipt)) }
func (q *radioReceipts) Pop() any          { old := *q; x := old[len(old)-1]; *q = old[:len(old)-1]; return x }

// Model the physical FIFO, variable full-body completion, compressed receipt
// callbacks and the actual pacer together. Unlike the older controller models,
// a pacing pause does not falsely mark the publication as flight-limited.
func TestRadioJitterWithPacingRetainsLearnedBulkPath(t *testing.T) {
	for _, capacity := range []int{7, 90} {
		t.Run(map[int]string{7: "slow", 90: "mobile"}[capacity], func(t *testing.T) {
			now := time.Unix(100, 0)
			w := deliveryWindow{adaptive: true}
			const quantum = 32 << 10
			for run := 0; run < 2; run++ {
				mark := w.sent(16<<10, now, true)
				now = now.Add(385 * time.Millisecond)
				w.delivered(16<<10, 385*time.Millisecond, now, true, mark)
				w.observeInteractive(250*time.Millisecond, now)
				start, finish := now, now
				var pending radioReceipts
				var all, tail, ordinal int
				var maxQueue time.Duration
				nextControl := start
				for now.Sub(start) < 16*time.Second {
					for len(pending) > 0 && !pending[0].at.After(now) {
						p := heap.Pop(&pending).(radioReceipt)
						w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
						all += quantum
						if now.Sub(start) >= 12*time.Second {
							tail += quantum
						}
					}
					if !now.Before(nextControl) {
						queue := finish.Sub(now)
						if queue < 0 {
							queue = 0
						}
						sample := 300*time.Millisecond + queue
						// A radio/provider tail does not imply extra bytes in the FIFO.
						if tick := int(now.Sub(start) / (100 * time.Millisecond)); tick == 25 || tick == 85 || tick == 145 {
							sample += 550 * time.Millisecond
						}
						w.observeInteractive(sample, now)
						nextControl = nextControl.Add(100 * time.Millisecond)
					}
					for {
						if w.flight+quantum > w.allowance() {
							w.limited = true
							break
						}
						if !w.pacer.ready(quantum, w.pacingRate(now), now) {
							break
						}
						if finish.Before(now) {
							finish = now
						}
						finish = finish.Add(time.Duration(quantum) * 8 * time.Second / time.Duration(capacity*1_000_000))
						maxQueue = max(maxQueue, finish.Sub(now))
						delay := []time.Duration{500 * time.Millisecond, 650 * time.Millisecond, 550 * time.Millisecond, 700 * time.Millisecond}[ordinal%4]
						ordinal++
						arrival := finish.Add(delay)
						at := start.Add((arrival.Sub(start) + 100*time.Millisecond - 1) / (100 * time.Millisecond) * (100 * time.Millisecond))
						heap.Push(&pending, radioReceipt{now, at, w.sent(quantum, now, true)})
						w.pacer.admitted(quantum)
					}
					now = now.Add(time.Millisecond)
				}
				average := float64(all) * 8 / 16e6
				last := float64(tail) * 8 / 4e6
				t.Logf("run%d capacity%d full%.2f tail%.2f maxqueue%s window%d", run+1, capacity, average, last, maxQueue, w.allowance())
				if last < float64(capacity)*.8 {
					t.Errorf("paced mobile transfer forgot usable full-body path: tail%.2f/%dMbps", last, capacity)
				}
				if maxQueue > 1500*time.Millisecond {
					t.Errorf("controller flooded finite FIFO: %s", maxQueue)
				}
				for len(pending) > 0 {
					p := heap.Pop(&pending).(radioReceipt)
					if now.Before(p.at) {
						now = p.at
					}
					w.delivered(quantum, now.Sub(p.sent), now, true, p.mark)
				}
				now = now.Add(5 * time.Second)
			}
		})
	}
}
