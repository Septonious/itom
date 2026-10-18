package tunnel

import (
	"fmt"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRC9ShortAuditIsConfinedToTwoActiveQuietReaders(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 0; i < 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	g.delivery.bulk = 1 // Keep the established quiet reserve stable under load.
	shortened := 0
	for _, l := range g.lanes {
		if got := g.interactiveReaderAuditDelay(l, 1, 500*time.Millisecond); got == readerResponseAuditDelay {
			shortened++
		} else if got != 500*time.Millisecond {
			t.Fatal("unexpected audit", got)
		}
		if got := g.interactiveReaderAuditDelay(l, 0, readerIdleAudit); got != readerIdleAudit {
			t.Fatal("idle account polled faster", got)
		}
		if got := g.interactiveReaderAuditDelay(l, 1, 50*time.Millisecond); got != 50*time.Millisecond {
			t.Fatal("existing notification-loss fallback delayed", got)
		}
	}
	if shortened != 2 {
		t.Fatal("audit burst escaped quiet reserve", shortened)
	}
	small := newStripeGroup(wire.ID{2}, 1)
	for i := 0; i < 4; i++ {
		l := stripeTestLane(small, fmt.Sprint(i), false)
		defer l.cancel()
		if err := small.attach(l); err != nil {
			t.Fatal(err)
		}
		if got := small.interactiveReaderAuditDelay(l, 1, 500*time.Millisecond); got != 500*time.Millisecond {
			t.Fatal("unreserved pool changed", got)
		}
	}
}

func TestRC9SplitShortAuditFollowsActualCarriersAndExpires(t *testing.T) {
	g := newStripeGroup(wire.ID{2}, 1)
	for i := 0; i < 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		l.dataShards = 1
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	g.noteShortReaderLocked("a03", now.Add(-2*time.Second))
	g.noteShortReaderLocked("a07", now.Add(-time.Second))
	g.noteShortReaderLocked("a09", now)
	for _, l := range g.lanes {
		got := g.interactiveReaderAuditDelay(l, 1, 500*time.Millisecond)
		want := 500 * time.Millisecond
		if l.account.ID == "a07" || l.account.ID == "a09" {
			want = readerResponseAuditDelay
		}
		if got != want {
			t.Fatal(l.account.ID, got, want)
		}
	}
	for i := range g.shortReaderActivity {
		g.shortReaderActivity[i].until = now.Add(-time.Second)
	}
	if got := g.interactiveReaderAuditDelay(g.lanes["a09"], 1, 500*time.Millisecond); got != 500*time.Millisecond {
		t.Fatal("short audit outlived real demand", got)
	}
}
