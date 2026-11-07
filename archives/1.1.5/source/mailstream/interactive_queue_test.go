package mailstream

import (
	"sort"
	"testing"
	"time"
)

// One shared 7 Mbps serialization queue carries MIME-expanded DATA and small
// request/reply probes. Bulk receipt and interactive response each make a real
// return trip through that queue; delayed body callbacks alone are tested
// separately. The simulator cannot give the controller an invented link rate.
func TestInteractiveReceiptsBoundSharedWeakLinkQueue(t *testing.T) {
	type event struct {
		sent, ready time.Time
		n           int
		bulk, reply bool
		mark        deliveryMark
	}
	const quantum = 32 << 10
	start := time.Unix(100, 0)
	now, finish, nextProbe := start, start, start
	w := deliveryWindow{adaptive: true}
	var pending []event
	schedule := func(e event, wireBytes int) {
		finish = maxTime(finish, now).Add(time.Duration(wireBytes) * 8 * time.Second / 7_000_000)
		e.ready = finish.Add(75 * time.Millisecond)
		pending = append(pending, e)
	}
	maximum := time.Duration(0)
	tail := 0
	probes := 0
	for now.Sub(start) < 30*time.Second {
		for w.flight+quantum <= w.allowance() {
			schedule(event{sent: now, n: quantum, bulk: true, mark: w.sent(quantum, now, true)}, quantum*137/100)
		}
		w.limited = true
		sort.SliceStable(pending, func(i, j int) bool { return pending[i].ready.Before(pending[j].ready) })
		if len(pending) == 0 {
			t.Fatal("no in-flight events")
		}
		if nextProbe.Before(pending[0].ready) {
			now = nextProbe
			nextProbe = nextProbe.Add(500 * time.Millisecond)
			w.sent(128, now, false)
			schedule(event{sent: now, n: 128}, 256)
			continue
		}
		e := pending[0]
		pending = pending[1:]
		now = e.ready
		if !e.reply {
			e.reply = true
			schedule(e, 256)
			continue
		}
		w.delivered(e.n, now.Sub(e.sent), now, e.bulk, e.mark)
		if !e.bulk && now.Sub(start) > 5*time.Second {
			maximum = max(maximum, now.Sub(e.sent))
			probes++
		}
		if e.bulk && now.Sub(start) > 25*time.Second {
			tail += e.n
		}
	}
	mbps := float64(tail) * 8 / 5e6
	if probes < 20 || maximum > 2*time.Second || mbps < 4 {
		t.Fatalf("interactive queue exceeded weak-link budget: probes=%d max=%s tail=%.1f Mbps", probes, maximum, mbps)
	}
	t.Logf("shared weak link: max interactive RTT=%s tail=%.1f Mbps", maximum, mbps)
}
