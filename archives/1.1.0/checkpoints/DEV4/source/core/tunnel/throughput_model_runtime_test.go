package tunnel

import (
	"context"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestThroughputModelRuntimeFactoriesPreserveExplicitLegacyComparator(t *testing.T) {
	legacy := newStripeGroup(wire.ID{1}, 1)
	if legacy.delivery.modelEnabled {
		t.Fatal("legacy comparator silently changed")
	}
	client := newModelStripeGroup(wire.ID{2}, 1)
	server := newModelStripeHub().group(wire.ID{2}, 1)
	if server == nil || !server.delivery.modelEnabled || !client.delivery.modelEnabled || client.delivery.model.retainWindow != modelRetainsWindow {
		t.Fatal("real endpoint does not select model")
	}
	rotated := newModelStripeGroup(wire.ID{3}, 2)
	if !rotated.delivery.modelEnabled {
		t.Fatal("rotated generation lost model")
	}
}

func TestThroughputModelSupervisorsSelectRuntime(t *testing.T) {
	f := newProductionSupervisorFixtureAccounts(t, 2)
	c := rev4Open(t, f)
	defer c.Close()
	if err := rev4Exchange(c, 1, 32, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if !f.client.stripe.delivery.modelEnabled {
		t.Fatal("real client kept the legacy controller")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.serverLanes {
		if l.stripe == nil || !l.stripe.delivery.modelEnabled {
			t.Fatal("real server kept the legacy controller")
		}
	}
}

func TestThroughputModelAdmissionPreservesSmallExplicitBudget(t *testing.T) {
	g := newModelStripeGroup(wire.ID{1}, 1)
	g.delivery.window = 48 << 10
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	r, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.jobs) != 1 || g.delivery.bulk != 32<<10 || g.delivery.window != 48<<10 {
		t.Fatal("controller expanded an explicit budget without a useful ACK", g.delivery.window, g.delivery.bulk, len(r.jobs))
	}
	r.finish(r.jobs, committed(r.jobs, flow.CommitUnknown))
	g.deliveryProgress(1, 16<<10)
	g.deliveryProgress(1, 16<<10)
	if g.delivery.bulk != 16<<10 || g.delivery.delivered != 16<<10 {
		t.Fatal("partial/duplicate ACK changed logical authority")
	}
	g.deliveryStopped(1)
	if g.delivery.bulk != 0 || g.delivery.delivered != 16<<10 {
		t.Fatal("retirement became useful delivery")
	}
}
