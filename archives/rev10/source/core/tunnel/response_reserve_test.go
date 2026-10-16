package tunnel

import (
	"fmt"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestShortRequestPrimesOnlyTwoQuietReceiveStripes(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	var owner *lane
	for i := 1; i <= 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
		if i == 10 {
			owner = l
		}
	}
	owner.rtt.Store(500)
	g.expectFastResponse(owner)
	for key, l := range g.lanes {
		hot := l.readHotUntil.Load() > time.Now().UnixNano()
		if hot != (key == "a01" || key == "a02") {
			t.Fatal("wrong receive reserve", key, hot)
		}
		if hot && time.Until(time.Unix(0, l.readHotUntil.Load())) < time.Second {
			t.Fatal("request budget still expires before observed RTT")
		}
	}
	for _, l := range g.lanes {
		l.readHotUntil.Store(0)
	}
	owner.side = true
	g.expectFastResponse(owner)
	for key, l := range g.lanes {
		if l.readHotUntil.Load() != 0 {
			t.Fatal("reply caused reciprocal polling", key)
		}
	}
}
func TestReaderResponseBudgetBoundedAndNeverShortened(t *testing.T) {
	l := &lane{}
	l.rtt.Store(1000000)
	if got := l.readerResponseBudget(450 * time.Millisecond); got != 2*time.Second {
		t.Fatal(got)
	}
	l.rtt.Store(20)
	if got := l.readerResponseBudget(650 * time.Millisecond); got != 650*time.Millisecond {
		t.Fatal(got)
	}
	l.readWake = make(chan struct{}, 1)
	l.armReaderHot(time.Second)
	until := l.readHotUntil.Load()
	l.armReaderHot(100 * time.Millisecond)
	if l.readHotUntil.Load() < until {
		t.Fatal("active window shortened")
	}
	<-l.readWake
	select {
	case <-l.readWake:
		t.Fatal("request burst did not coalesce wake")
	default:
	}
	l.armReaderHot(100 * time.Millisecond)
	select {
	case <-l.readWake:
	default:
		t.Fatal("fresh request waited for old empty poll despite active window")
	}
}
