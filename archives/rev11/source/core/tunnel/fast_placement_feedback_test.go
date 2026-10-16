package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestFastCarrierPlacementUsesACKNotAppendCompletion(t *testing.T) {
	g := newStripeGroup(wire.ID{3}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		g.attach(l)
	}
	var jobs [][]*datapath.Job
	for index, id := range []uint64{1, 3} {
		j := deliveryJobs(t, id, 1, 80)
		jobs = append(jobs, j)
		r, err := g.reserveDelivery(context.Background(), j)
		if err != nil {
			t.Fatal(err)
		}
		g.bindDeliveryCarrier(j, fmt.Sprintf("a%02d", index+1))
		g.bindDeliveryCarrier(j, fmt.Sprintf("a%02d", index+1))
		r.finish(j, committed(j, flow.Committed))
	}
	keys := []string{"a01", "a02", "a03"}
	g.mu.Lock()
	g.prioritizeFastTargetsLocked(keys, nil)
	g.mu.Unlock()
	if keys[0] != "a03" || g.delivery.fastCarrierRanges["a01"] != 1 || g.delivery.fastCarrierRanges["a02"] != 1 {
		t.Fatal("tagged APPEND hid the unread fast range", keys, g.delivery.fastCarrierRanges)
	}
	g.deliveryProgress(1, 40)
	if g.delivery.fastCarrierRanges["a01"] != 1 {
		t.Fatal("partial ACK erased placement cost")
	}
	g.deliveryProgress(1, 80)
	g.deliveryProgress(3, 80)
	if len(g.delivery.fastCarrierRanges) != 0 || g.delivery.fast != 0 {
		t.Fatal("logical ACK left stale placement cost", g.delivery.fastCarrierRanges, g.delivery.fast)
	}
}

func TestFastCarrierFailureAndRetirementDoNotInventDelivery(t *testing.T) {
	g := newStripeGroup(wire.ID{4}, 1)
	j := deliveryJobs(t, 1, 1, 80)
	r, err := g.reserveDelivery(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	g.bindDeliveryCarrier(j, "a01")
	out := committed(j, flow.NotCommitted)
	out[0].NonFatal = true
	r.finish(j, out)
	if len(g.delivery.fastCarrierRanges) != 0 || g.delivery.fast != 80 || g.delivery.delivered != 0 {
		t.Fatal("failed publication changed logical debt")
	}
	r, err = g.reserveDelivery(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	g.bindDeliveryCarrier(j, "a02")
	r.finish(j, committed(j, flow.CommitUnknown))
	if g.delivery.fastCarrierRanges["a02"] != 1 {
		t.Fatal("unknown publication was treated as delivered")
	}
	g.delivery.progress(1, 40, time.Now())
	g.delivery.retire(1)
	if len(g.delivery.fastCarrierRanges) != 0 || g.delivery.fast != 0 || g.delivery.delivered != 40 {
		t.Fatal("retirement fabricated useful delivery")
	}
}

func TestSpilledShortRequestPrimesItsPhysicalReader(t *testing.T) {
	g := newStripeGroup(wire.ID{5}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		g.attach(l)
	}
	g.expectFastResponseOn(g.lanes["a05"], g.lanes["a03"])
	for id, l := range g.lanes {
		hot := l.readHotUntil.Load() > time.Now().UnixNano()
		if hot != (id == "a01" || id == "a02" || id == "a03") {
			t.Fatal("wrong readers primed for spill", id, hot)
		}
	}
}
