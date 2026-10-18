package tunnel

import (
	"context"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRC2BulkFeedbackEscapesStalledAffinity(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b := stripeTestLane(g, "a01", true), stripeTestLane(g, "a02", true)
	for _, l := range []*lane{a, b} {
		defer l.cancel()
		l.controlQueue = make(chan queuedControl, 8)
		l.urgentQueue = make(chan queuedControl, 8)
		l.maintenanceQueue = make(chan queuedControl, 8)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.register(7, a); err != nil {
		t.Fatal(err)
	}
	f, _ := wire.EncodeControl(7, wire.Progress{Receipt: 128 << 10, MaxStream: runtimeStreamWindow + (128 << 10), MaxData: runtimeReceiveBytes + (128 << 10)})
	if err := g.sendStreamControl(context.Background(), a, f); err != nil {
		t.Fatal(err)
	}
	chosen := g.feedbackAffinity[7]
	stalled := g.lanes[chosen]
	<-stalled.controlQueue
	stalled.controlWriting.Store(1)
	stalled.controlWriteStarted.Store(time.Now().Add(-2 * time.Second).UnixNano())
	f, _ = wire.EncodeControl(7, wire.Progress{Receipt: 256 << 10, MaxStream: runtimeStreamWindow + (256 << 10), MaxData: runtimeReceiveBytes + (256 << 10)})
	if err := g.sendStreamControl(context.Background(), a, f); err != nil {
		t.Fatal(err)
	}
	if g.feedbackAffinity[7] == chosen || len(stalled.controlQueue) != 0 {
		t.Fatal("bulk feedback stayed pinned behind stalled APPEND")
	}
	if !stalled.streamCarrierReady() || stalled.controlDebt.Load() != 0 {
		t.Fatal("placement retired carrier or changed debt")
	}
}

func TestRC2StallPenaltyNeedsElapsedWorkAndClearsOnCompletion(t *testing.T) {
	now := time.Now()
	l := &lane{}
	l.controlWriteStarted.Store(now.Add(-10 * time.Second).UnixNano())
	if l.controlStallPenalty(now) != 0 {
		t.Fatal("quiet socket treated as stalled")
	}
	l.controlWriting.Store(1)
	l.controlWriteStarted.Store(now.Add(-900 * time.Millisecond).UnixNano())
	if l.controlStallPenalty(now) != 0 {
		t.Fatal("normal in-flight turn penalized")
	}
	l.controlWriteStarted.Store(now.Add(-2 * time.Second).UnixNano())
	if l.controlStallPenalty(now) == 0 {
		t.Fatal("long in-flight turn concealed")
	}
	l.idleRTT.Store(1000)
	if l.controlStallPenalty(now) != 0 {
		t.Fatal("ordinary high propagation classified as stalled")
	}
	l.controlWriteStarted.Store(now.Add(-4 * time.Second).UnixNano())
	if l.controlStallPenalty(now) == 0 {
		t.Fatal("high-RTT stall concealed")
	}
	l.controlWriting.Store(0)
	if l.controlStallPenalty(now) != 0 {
		t.Fatal("completed operation retained penalty")
	}
}

func TestRC2StalledFeedbackStillHasLastResortAndOpenAvoidsIt(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b := stripeTestLane(g, "a01", false), stripeTestLane(g, "a02", false)
	for _, l := range []*lane{a, b} {
		defer l.cancel()
		l.markRemoteProgress()
		l.rtt.Store(300)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	a.controlWriting.Store(1)
	a.controlWriteStarted.Store(now.Add(-2 * time.Second).UnixNano())
	ready := []*lane{a, b}
	if n := rankOpenLanes(ready, now); n != 1 || ready[0] != b {
		t.Fatal("new OPEN selected stalled control writer", n)
	}
	f, _ := wire.EncodeControl(1, wire.Progress{Receipt: 128 << 10})
	got := g.orderBulkFeedbackLocked([]*lane{a}, f)
	if len(got) != 1 || got[0] != a {
		t.Fatal("sole usable carrier was excluded")
	}
}
