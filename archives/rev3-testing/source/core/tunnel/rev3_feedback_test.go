package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/wire"
	"testing"
)

func TestRev3BulkFeedbackCoalescesDespiteIndependentDataOccupancy(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		t.Cleanup(l.cancel)
		l.dataShards = 1
		l.controlQueue = make(chan queuedControl, 64)
		l.urgentQueue = make(chan queuedControl, 64)
		l.maintenanceQueue = make(chan queuedControl, 64)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	owner := g.lanes["a01"]
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	f, _ := wire.EncodeControl(1, wire.Progress{Receipt: 128 << 10, MaxStream: 2 << 20, MaxData: 32 << 20})
	if err := g.sendStreamControl(context.Background(), owner, f); err != nil {
		t.Fatal(err)
	}
	first := g.feedbackAffinity[1]
	carrier := g.lanes[first]
	// The first snapshot is already being APPENDed. New DATA occupancy on its
	// separate socket must not scatter the next snapshot to another account.
	item := <-carrier.controlQueue
	carrier.resolveFlowControl(item)
	carrier.physicalSlots <- struct{}{}
	f, _ = wire.EncodeControl(1, wire.Progress{Receipt: 160 << 10, MaxStream: 2 << 20, MaxData: 32 << 20})
	if err := g.sendStreamControl(context.Background(), owner, f); err != nil {
		t.Fatal(err)
	}
	if g.feedbackAffinity[1] != first {
		t.Fatal("independent DATA occupancy migrated feedback")
	}
}

func TestRev3PairedStreamsUseWholeResponsiveFeedbackPool(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 15; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		t.Cleanup(l.cancel)
		l.dataShards = 1
		l.controlQueue = make(chan queuedControl, 64)
		l.urgentQueue = make(chan queuedControl, 64)
		l.maintenanceQueue = make(chan queuedControl, 64)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	owner := g.lanes["a01"]
	seen := map[string]bool{}
	for id := uint64(1); id < 96; id += 4 {
		if err := g.register(id, owner); err != nil {
			t.Fatal(err)
		}
		f, _ := wire.EncodeControl(id, wire.Progress{Receipt: 128 << 10, MaxStream: 2 << 20, MaxData: 32 << 20})
		if err := g.sendStreamControl(context.Background(), owner, f); err != nil {
			t.Fatal(err)
		}
		seen[g.feedbackAffinity[id]] = true
	}
	if len(seen) < 8 {
		t.Fatal("paired one-direction streams collapse onto a few feedback paths", len(seen))
	}
}
