package tunnel

import (
	"context"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRev3IsolatedOldRangeDoesNotDescribeHealthyMultiplexQueue(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.policy.lastProgress = now
	d.bulkDelivered = 32 << 10
	old := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now.Add(-4 * time.Second)}
	fresh := &deliveryRange{key: deliveryKey{3, 0}, end: 512 << 10, admitted: now.Add(-300 * time.Millisecond)}
	d.ranges[old.key] = old
	d.ranges[fresh.key] = fresh
	d.bulk = old.end + fresh.end
	before := d.window
	d.maintain(now, true)
	if d.window < before {
		t.Fatal("one old stream throttled healthy siblings", d.window)
	}
	// Collective loss still bounds admission, keeping every unknown range charged.
	d.lastDecision = now
	d.maintain(now.Add(4*time.Second), true)
	if d.window >= before || d.bulk != old.end+fresh.end {
		t.Fatal("collective outage ignored or unknown debt erased")
	}
}
func TestRev3RepairACKDoesNotSampleItsOldSchedulingDelay(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now.Add(-4 * time.Second), flightAtAdmission: 32 << 10}
	d.ranges[r.key] = r
	d.bulk = r.end
	d.invalidateRepairFloor(1, 0, r.end)
	d.progress(1, r.end, now)
	if d.ageCount != 0 || d.lowFlightCount != 0 {
		t.Fatal("repair delay poisoned fresh capacity samples")
	}
	if d.delivered != 32<<10 || d.bulk != 0 {
		t.Fatal("useful repaired bytes were not counted exactly once")
	}
}
func TestRev3SlowStreamCannotOccupyMostMultiplexFlight(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	now := time.Now()
	for id := uint64(3); id <= 47; id += 2 {
		r := &deliveryRange{key: deliveryKey{id, 0}, end: 32 << 10, admitted: now}
		g.delivery.ranges[r.key] = r
		g.delivery.bulk += r.end
	}
	jobs := deliveryJobs(t, 1, 8, 32<<10)
	r, err := g.reserveDeliveryBatch(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.jobs) > 2 {
		t.Fatal("one stream filled shared flight while 23 others waited", len(r.jobs))
	}
	r.finish(r.jobs, nil)
}
