package tunnel

import (
	"context"
	"errors"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRev3DeliveryRotatesScarceCreditBetweenStreams(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.delivery.window = deliveryMinWindow
	a := deliveryJobs(t, 1, 2, 32<<10)
	b := deliveryJobs(t, 3, 2, 32<<10)
	batch := append(append([]*datapath.Job{}, a...), b...)
	r, err := g.reserveDelivery(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	first := map[uint64]bool{}
	for _, j := range r.jobs {
		first[j.Attempt().Stream] = true
	}
	if len(first) != 2 {
		t.Fatal("scarce credit admitted only one stream", first)
	}
	r.finish(r.jobs, committed(r.jobs, flow.Committed))
}
func TestRev3StoppedMemberDoesNotRejectLiveBatchPrefix(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.deliveryStopped(1)
	a := deliveryJobs(t, 1, 1, 32<<10)
	b := deliveryJobs(t, 3, 1, 32<<10)
	r, err := g.reserveDelivery(context.Background(), append(a, b...))
	if err != nil {
		t.Fatal("closed stream rejected sibling", err)
	}
	if len(r.jobs) != 1 || r.jobs[0] != b[0] {
		t.Fatal("wrong live selection")
	}
	r.finish(r.jobs, nil)
}

func TestRev3BatchYieldsFullFlightAndAdmittedRetryKeepsWorker(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.delivery.window = deliveryMinWindow
	charged := deliveryJobs(t, 1, 2, 32<<10)
	r, err := g.reserveDeliveryBatch(context.Background(), charged)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := committed(charged, flow.NotCommitted)
	for i := range outcomes {
		outcomes[i].NonFatal = true
	}
	r.finish(charged, outcomes)
	// Eight fresh batches must return to the actor, allowing the unpublished
	// charged prefix to get a worker without a receipt that cannot yet exist.
	for id := uint64(3); id < 19; id += 2 {
		jobs := deliveryJobs(t, id, 1, 32<<10)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, err = g.reserveDeliveryBatch(ctx, jobs)
		cancel()
		if !errors.Is(err, errDeliveryDeferred) {
			t.Fatalf("worker parked on full flight: %v", err)
		}
		if wake := g.deliveryRetryWake(jobs[0]); wake == nil {
			t.Fatal("missing bounded retry wake")
		}
	}
	retry, err := g.reserveDeliveryBatch(context.Background(), charged)
	if err != nil {
		t.Fatal(err)
	}
	if g.delivery.bulk != deliveryMinWindow || len(g.delivery.waiters) != 0 {
		t.Fatal("yield erased or duplicated logical flight")
	}
	retry.finish(charged, committed(charged, flow.CommitUnknown))
	if g.delivery.delivered != 0 || g.delivery.bulk != deliveryMinWindow {
		t.Fatal("unknown publication invented delivery")
	}
}

func TestRev3HoleAgeCannotExtendItsOwnVisibilityGrace(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now.Add(-8 * time.Second), accepted: now.Add(-4 * time.Second)}
	d.ranges[r.key] = r
	d.bulk = 32 << 10
	d.observedAgeMS = 8000
	d.baselineMS = 300
	if d.deferAcceptedRepair(1, 0, 32<<10, now) {
		t.Fatal("stalled hole extends its own repair delay")
	}
}

func TestRev3WarmWindowDoesNotAllocateIdleSpeculativeBacklog(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.delivery.window = deliveryMaxWindow
	g.delivery.bulk = 2 << 20
	l := &lane{stripe: g}
	if got := l.bulkAdmissionLimit(); got > 2<<20+datapath.MaxBulkWriters*datapath.BulkBatchTargetBytes || got > runtimeLogicalBytes-runtimeInteractiveReserve {
		t.Fatal("unused learned flight window created stale speculative actor work", got)
	}
	if g.delivery.bulk != 2<<20 || g.delivery.window != deliveryMaxWindow {
		t.Fatal("queue sizing changed charged flight or learned budget")
	}
}
