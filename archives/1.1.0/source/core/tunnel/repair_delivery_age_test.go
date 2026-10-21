package tunnel

import (
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestAcceptedRepairWaitsForDeliveryAndUnknownRemainsEligible(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.baselineMS = 300
	d.observedAgeMS = 4200
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 64 << 10, admitted: now.Add(-time.Second), accepted: now}
	d.ranges[r.key] = r
	d.bulk = r.end
	if !d.deferAcceptedRepair(1, 16<<10, 32<<10, now.Add(time.Second)) {
		t.Fatal("NACK amplified an accepted BODY still in transport")
	}
	wake := d.wake
	d.maintain(now.Add(5*time.Second), false)
	select {
	case <-wake:
	default:
		t.Fatal("bounded repair wake lost")
	}
	if d.deferAcceptedRepair(1, 0, 64<<10, now.Add(11*time.Second)) {
		t.Fatal("accepted loss recovery is unbounded")
	}
	r.accepted = time.Time{}
	if d.deferAcceptedRepair(1, 0, 64<<10, now) {
		t.Fatal("unknown publication was delayed as accepted")
	}
	if d.bulk != 64<<10 || d.delivered != 0 {
		t.Fatal("repair pacing refunded debt")
	}
}
func TestHighRTTBootstrapProbesWithoutCarrierMultiplier(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.observeProviderBaseline(300 * time.Millisecond)
	if g.delivery.window != 256<<10 {
		t.Fatal("high RTT burst", g.delivery.window)
	}
	g.delivery.delivered = 1
	g.observeProviderBaseline(time.Second)
	if g.delivery.window != 256<<10 {
		t.Fatal("later baseline reset learned capacity")
	}
}

func TestRev4RepairMeasuresRecoveredPathWithoutRefundingFlight(t *testing.T) {
	now := time.Now()
	d := newDeliveryController()
	d.window = 256 << 10
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 64 << 10,
		admitted: now.Add(-40 * time.Second), repaired: true, repairAccepted: now.Add(-200 * time.Millisecond)}
	d.ranges[r.key] = r
	d.bulk = r.end
	d.bulkDelivered, d.delivered = 4096, 4096
	d.maintain(now, true)
	if d.observedAgeMS != 200 || d.oldestAgeMS != 40000 || d.window < 256<<10 {
		t.Fatal("old outage poisoned recovered path feedback", d.observedAgeMS, d.oldestAgeMS, d.window)
	}
	if d.bulk != r.end || len(d.ranges) != 1 {
		t.Fatal("repair refunded an unacknowledged range")
	}
	// A physical repair that is itself stalled still has the normal outage guard.
	d.maintain(now.Add(4*time.Second), true)
	if d.policy.mode != deliveryOutage || d.window >= 256<<10 {
		t.Fatal("repaired physical loss bypassed outage protection", d.policy.mode, d.window)
	}
}

func TestMobileACKFloorKeepsServiceAndExcessDelayRecovers(t *testing.T) {
	d := newDeliveryController()
	d.baselineMS = 400
	now := time.Now()
	d.window = 512 << 10
	d.bulkDelivered = 65536
	d.delivered = 65536
	d.lastFastACKAt = now
	d.lastFastACKAgeMS = 850
	d.ageCount = 4
	d.lowFlightCount = 4
	for i := 0; i < 4; i++ {
		d.ages[i] = 900 * time.Millisecond
		d.lowFlightAges[i] = 900 * time.Millisecond
	}
	d.maintain(now, true)
	if d.window < 512<<10 || d.fastPressureDecisions != 0 {
		t.Fatal("healthy measured path was treated as queue pressure", d.window, d.fastPressureDecisions)
	}
	before := d.window
	late := &deliveryRange{key: deliveryKey{999, 0}, end: 1024, fast: true, admitted: now.Add(time.Second - 2900*time.Millisecond)}
	d.ranges[late.key] = late
	d.fast += 1024
	d.progress(999, 1024, now.Add(time.Second))
	d.maintain(now.Add(time.Second), true)
	if d.window >= before {
		t.Fatal("real short-DATA queue pressure was ignored")
	}
}
