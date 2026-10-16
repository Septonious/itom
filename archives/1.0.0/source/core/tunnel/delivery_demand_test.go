package tunnel

import (
	"testing"
	"time"

	"itom.local/core/wire"
)

// Deferred DATA jobs leave the admission waiter list. Maintenance must consume
// demand observed earlier in its epoch, rather than depend on which goroutine
// happens to take the mutex at the decision boundary.
func TestDeliveryMaintenanceRemembersDemandForOneEpoch(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.lastDecision = now
	d.maintain(now.Add(100*time.Millisecond), true)
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 500 * time.Millisecond
	}
	d.maintain(now.Add(time.Second), false)
	if d.window != deliveryInitialWindow+64<<10 {
		t.Fatalf("maintenance lost preceding bulk demand: window=%d", d.window)
	}
	// Consumed demand cannot grow an idle session on stale samples.
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 500 * time.Millisecond
	}
	d.maintain(now.Add(2*time.Second), false)
	if d.window != deliveryInitialWindow || d.ageCount != 0 {
		t.Fatal("demand persisted into an idle epoch", d.window, d.ageCount)
	}
}

func TestDeliveryRememberedDemandCannotHideStall(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.window = deliveryMaxWindow
	d.lastDecision = now
	d.maintain(now.Add(100*time.Millisecond), true)
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 2 * time.Second
	}
	d.maintain(now.Add(time.Second), false)
	if d.window != deliveryMaxWindow/2 {
		t.Fatal("remembered demand overrode late ACK shrink", d.window)
	}
}

func TestDeliveryDemandExpiresAcrossInactiveEpoch(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.lastDecision = now
	d.maintain(now.Add(100*time.Millisecond), true)
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 500 * time.Millisecond
	}
	d.maintain(now.Add(3*time.Second), false)
	if d.window != deliveryInitialWindow || d.ageCount != 0 {
		t.Fatal("old demand grew an idle session", d.window, d.ageCount)
	}
}

func TestDeliveryInteractiveWaiterDoesNotCreateBulkDemand(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	d := &g.delivery
	now := time.Now()
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now}
	d.ranges[r.key] = r
	d.bulk = r.end
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 500 * time.Millisecond
	}
	d.waiters = []*deliveryWaiter{{fast: true}, {repair: true}}
	g.maintainDelivery(now.Add(500 * time.Millisecond))
	if d.window != deliveryInitialWindow {
		t.Fatal("interactive/repair work created bulk demand", d.window)
	}
}
