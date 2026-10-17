package tunnel

import (
	"fmt"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRC4UnscopedHotPollingUsesRC2Backoff(t *testing.T) {
	idle := 0
	for _, want := range []time.Duration{10, 20, 40, 80, 80} {
		if got := readerPollDelay(1, true, false, 0, &idle); got != want*time.Millisecond {
			t.Fatal("unscoped hot polling lost RC2 pressure bound", got, want)
		}
	}
	if d := readerPollDelay(0, false, false, 0, &idle); d != 2*time.Second {
		t.Fatal("hot polling changed quiet transition", d)
	}
}

// The setup helper is also used by the contextual reserve tests below.
func rc4ReaderGroup(t *testing.T) *stripeGroup {
	t.Helper()
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 6; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		t.Cleanup(l.cancel)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestRC4RapidPollingRequiresQuietReserveAndLightDemand(t *testing.T) {
	g := rc4ReaderGroup(t)
	count := func() int {
		n := 0
		for _, l := range g.lanes {
			if g.allowRapidReader(l) {
				n++
			}
		}
		return n
	}
	if n := count(); n != 2 {
		t.Fatal("rapid polling reserve not bounded to two", n)
	}
	g.delivery.bulk = 1
	if n := count(); n != 0 {
		t.Fatal("bulk flight allowed rapid polling", n)
	}
	g.delivery.bulk = 0
	g.delivery.waiters = []*deliveryWaiter{{fast: false}}
	if n := count(); n != 0 {
		t.Fatal("queued bulk allowed rapid polling", n)
	}
	g.delivery.waiters = nil
	for i := uint64(1); i <= 3; i++ {
		g.delivery.ranges[deliveryKey{i, 0}] = &deliveryRange{fast: true}
	}
	if n := count(); n != 0 {
		t.Fatal("parallel short burst allowed rapid polling", n)
	}
	delete(g.delivery.ranges, deliveryKey{3, 0})
	if n := count(); n != 2 {
		t.Fatal("light request demand could not resume", n)
	}
}

func TestRC4RapidDelayNeverChangesQuietOrBacklogBehavior(t *testing.T) {
	for _, rapid := range []bool{false, true} {
		idle := 4
		if d := readerResponsePollDelay(0, false, false, 0, &idle, rapid); d != 2*time.Second {
			t.Fatal("quiet cadence changed", rapid, d)
		}
		if d := readerResponsePollDelay(1, true, true, 1, &idle, rapid); d != 0 || idle != 0 {
			t.Fatal("visible backlog was paced", rapid, d)
		}
	}
	idle := 4
	if d := readerResponsePollDelay(1, true, false, 0, &idle, true); d != 20*time.Millisecond || idle != 4 {
		t.Fatal("fast turn corrupted shared idle history", d, idle)
	}
}
