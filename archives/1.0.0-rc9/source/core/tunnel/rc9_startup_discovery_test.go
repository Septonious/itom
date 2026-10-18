package tunnel

import (
	"context"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRC9UnprovenDiscoveryPacesWithoutStrandingIndependentStreams(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	old := deliveryJobs(t, 1, 6, 32<<10)
	r, err := g.reserveDeliveryBatch(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	r.finish(r.jobs, committed(r.jobs, flow.Committed))
	g.delivery.window = deliveryMinWindow
	first, err := g.reserveDeliveryBatch(context.Background(), deliveryJobs(t, 3, 1, 32<<10))
	if err != nil {
		t.Fatal("first independent probe blocked", err)
	}
	first.finish(first.jobs, committed(first.jobs, flow.Committed))
	later := deliveryJobs(t, 5, 1, 32<<10)
	if _, err := g.reserveDeliveryBatch(context.Background(), later); err == nil {
		t.Fatal("new streams bypassed the shared window as a burst")
	}
	wake := g.delivery.wake
	charged := g.delivery.bulk
	g.maintainDelivery(time.Now().Add(2 * deliveryDecisionEpoch))
	select {
	case <-wake:
	default:
		t.Fatal("paced new stream lost its timer wake")
	}
	if g.delivery.bulk != charged || g.delivery.delivered != 0 {
		t.Fatal("timer invented ACK/credit")
	}
	second, err := g.reserveDeliveryBatch(context.Background(), later)
	if err != nil {
		t.Fatal("unseen BODY on old streams stranded an independent new stream", err)
	}
	second.finish(second.jobs, committed(second.jobs, flow.Committed))
	if g.delivery.bulk != 256<<10 || len(g.delivery.ranges) != 8 || g.delivery.delivered != 0 {
		t.Fatal("charged flight changed", g.delivery.bulk, len(g.delivery.ranges))
	}
	g.deliveryProgress(3, 32<<10)
	// Proven independent progress keeps its escape immediately, not on a timer.
	more := deliveryJobs(t, 3, 2, 32<<10)[1:]
	if _, err := g.reserveDeliveryBatch(context.Background(), more); err != nil {
		t.Fatal("healthy stream held behind cold discovery", err)
	}
}

func TestRC9LowNOOPDoesNotAssumeHighLinkCapacity(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.observeProviderBaseline(time.Millisecond)
	if g.delivery.window != 256<<10 || g.delivery.initialWindow != 256<<10 {
		t.Fatal("low NOOP admitted a capacity-unmeasured MiB", g.delivery.window)
	}
	g.delivery.delivered = 1
	g.delivery.window = 4 << 20
	g.observeProviderBaseline(300 * time.Millisecond)
	if g.delivery.window != 4<<20 {
		t.Fatal("baseline reset learned useful capacity")
	}
	if g.delivery.maxWindow != deliveryMaxWindow {
		t.Fatal("hard flight ceiling changed")
	}
}

func TestRC9ColdFullWindowCannotInflateUnloadedACKFloor(t *testing.T) {
	for _, split := range []bool{false, true} {
		g := newStripeGroup(wire.ID{1}, 1)
		g.observeProviderBaseline(300 * time.Millisecond)
		jobs := deliveryJobs(t, 1, 8, 32<<10)
		batches := [][]*datapath.Job{jobs}
		if split {
			batches = [][]*datapath.Job{jobs[:1], jobs[1:]}
		}
		for _, batch := range batches {
			r, err := g.reserveDeliveryBatch(context.Background(), batch)
			if err != nil {
				t.Fatal(err)
			}
			r.finish(r.jobs, committed(r.jobs, flow.Committed))
		}
		g.deliveryProgress(1, 256<<10)
		if g.delivery.lowFlightCount != 0 || g.delivery.acknowledgementFloor() != 0 {
			t.Fatal("loaded cold flight calibrated the unloaded floor", split, g.delivery.lowFlightCount)
		}
		if g.delivery.bulk != 0 || g.delivery.bulkDelivered != 256<<10 {
			t.Fatal("calibration changed authenticated credit")
		}
	}
}
