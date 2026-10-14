package tunnel

import (
	"context"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestDeliveryLowFlightACKFloorBoundsAdditionalQueueDelay(t *testing.T) {
	g := newStripeGroup(wire.ID{3}, 2)
	jobs := deliveryJobs(t, 1, 4, 32<<10)
	reservation, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	reservation.finish(jobs, committed(jobs, flow.Committed))
	now := time.Now()
	for _, r := range g.delivery.ranges {
		r.admitted = now
	}
	g.delivery.progress(1, 128<<10, now.Add(850*time.Millisecond))
	initial := g.delivery.window
	g.delivery.maintain(now.Add(time.Second), true)
	if g.delivery.window > initial {
		t.Fatal("late low-flight ACK expanded the fixed latency budget", g.delivery.window)
	}
	if g.delivery.targetAge() != deliveryTargetAge {
		t.Fatal(g.delivery.targetAge())
	}
	if g.delivery.bulk != 0 || g.delivery.delivered != 128<<10 {
		t.Fatal("floor changes delivery accounting")
	}
}

func TestDeliveryFloorCannotLearnStalledFullWindowOrRepair(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	for i := 0; i < 8; i++ {
		id := uint64(i*2 + 1)
		r := &deliveryRange{key: deliveryKey{id, 0}, end: 32 << 10, admitted: now, flightAtAdmission: deliveryInitialWindow}
		d.ranges[r.key] = r
		d.bulk += 32 << 10
		d.progress(id, 32<<10, now.Add(10*time.Second))
	}
	if d.acknowledgementFloor() != 0 || d.targetAge() != deliveryTargetAge {
		t.Fatal("full backlog inflates target", d.targetAge())
	}
	repair := &deliveryRange{key: deliveryKey{101, 0}, start: 0, end: 32 << 10, admitted: now, flightAtAdmission: 32 << 10}
	d.ranges[repair.key] = repair
	d.bulk += 32 << 10
	d.invalidateRepairFloor(101, 0, 32<<10)
	d.progress(101, 32<<10, now.Add(10*time.Second))
	if d.lowFlightCount != 0 {
		t.Fatal("repair ACK contributes to floor")
	}
	d.lowFlightCount = 8
	for i := range d.lowFlightAges {
		d.lowFlightAges[i] = 10 * time.Second
	}
	if d.targetAge() != deliveryTargetAge {
		t.Fatal("unbounded target", d.targetAge())
	}
	d.window = deliveryMaxWindow
	d.ageCount = 4
	for i := 0; i < 4; i++ {
		d.ages[i] = 2800 * time.Millisecond
	}
	d.policy.delivered = d.bulkDelivered // no fresh ACK in this decision turn
	d.maintain(now.Add(11*time.Second), true)
	if d.window >= deliveryMaxWindow {
		t.Fatal("late acknowledgement expanded the admission ceiling")
	}
	if d.delivered != 9*(32<<10) || d.bulk != 0 {
		t.Fatal("flight accounting changed")
	}
}
