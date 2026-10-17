package tunnel

import (
	"context"
	"testing"
	"time"

	"itom.local/core/flow"
	"itom.local/core/wire"
)

func TestRev4DelayedStreamCannotBlockHealthyStreamPrefix(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	old := deliveryJobs(t, 1, 6, 32<<10)
	r, err := g.reserveDeliveryBatch(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	r.finish(r.jobs, committed(r.jobs, flow.Committed))
	g.mu.Lock()
	g.delivery.window = deliveryMinWindow
	g.mu.Unlock()
	fresh := deliveryJobs(t, 3, 2, 32<<10)
	x, err := g.reserveDeliveryBatch(context.Background(), fresh)
	if err != nil {
		t.Fatal("delayed flight on another TCP blocked a healthy stream", err)
	}
	if len(x.jobs) != 1 || x.jobs[0].Attempt().Offset != 0 {
		t.Fatal("discovery allowance exceeded one in-order frame", len(x.jobs))
	}
	x.finish(x.jobs, committed(x.jobs, flow.Committed))
	if g.delivery.bulk != 224<<10 || g.delivery.delivered != 0 {
		t.Fatal("stream isolation forgave outstanding delivery", g.delivery.bulk)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = g.reserveDelivery(ctx, fresh[1:]); err == nil {
		t.Fatal("an unacknowledged probe admitted a second frame")
	}
	g.deliveryProgress(3, 32<<10)
	y, err := g.reserveDeliveryBatch(context.Background(), fresh[1:])
	if err != nil {
		t.Fatal("authenticated healthy progress could not continue independently", err)
	}
	y.finish(y.jobs, committed(y.jobs, flow.Committed))
	if g.delivery.bulk != 224<<10 || g.delivery.delivered != 32<<10 {
		t.Fatal("independent progress changed blocked stream debt")
	}
	// The hard session ceiling still applies to a new stream's first frame.
	g.mu.Lock()
	g.delivery.bulk = deliveryMaxWindow
	g.mu.Unlock()
	if _, err = g.reserveDeliveryBatch(context.Background(), deliveryJobs(t, 5, 1, 32<<10)); err == nil {
		t.Fatal("discovery bypassed hard flight capacity")
	}
}
