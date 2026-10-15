package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestConcurrentShortStartupCannotCalibrateOrThrottleBulk(t *testing.T) {
	g := newStripeGroup(wire.ID{2}, 1)
	for _, id := range []uint64{1, 3} {
		jobs := deliveryJobs(t, id, 1, 1024)
		r, err := g.reserveDelivery(context.Background(), jobs)
		if err != nil {
			t.Fatal(err)
		}
		r.finish(jobs, committed(jobs, flow.Committed))
	}
	now := time.Now()
	for _, r := range g.delivery.ranges {
		r.admitted = now
	}
	g.delivery.progress(1, 1024, now.Add(1200*time.Millisecond))
	g.delivery.progress(3, 1024, now.Add(1300*time.Millisecond))
	if g.delivery.fastBaselineCount != 0 || !g.delivery.fastPressureUntil.IsZero() {
		t.Fatal("parallel startup mistaken for unloaded propagation or bulk queue", g.delivery.fastBaselineCount, g.delivery.fastPressureUntil)
	}
}

func TestBatchedACKGapCannotHalveHealthyBulkFlight(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	f := deliveryFeedback{Window: deliveryInitialWindow, Flight: 256 << 10, Demand: true, Delivered: 256 << 10, RateMbps: 8, Age: 600 * time.Millisecond, Target: deliveryTargetAge, Samples: 4}
	w := p.next(now, f)
	initial := w
	f.Window, f.Samples = w, 0
	w = p.next(now.Add(deliveryDecisionEpoch), f)
	if w != initial {
		t.Fatalf("250 ms ACK gap treated as congestion: %d", w)
	}
	f.Window = w
	f.Age = 2250 * time.Millisecond
	w = p.next(now.Add(2*time.Second), f)
	if w >= f.Window {
		t.Fatal("persistent stall no longer constrains flight")
	}
}

func TestNewBulkEpochDoesNotReuseSaturatedPriorFlight(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	w := p.next(now, deliveryFeedback{Window: deliveryMaxWindow, Target: deliveryTargetAge})
	if w > deliveryInitialWindow {
		t.Fatal("idle phase retained a saturated burst budget", w)
	}
	w = p.next(now.Add(time.Second), deliveryFeedback{Window: w, Flight: 32 << 10, Demand: true, Delivered: 65536, RateMbps: 8, Age: 300 * time.Millisecond, Target: deliveryTargetAge, Samples: 4})
	if w <= deliveryInitialWindow {
		t.Fatal("new epoch cannot discover capacity", w)
	}
}

func TestWindowLimitedProbeCanDiscoverCapacityWithoutEightMbpsFirst(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	w := uint64(256 << 10)
	delivered := uint64(0)
	for i := 0; i < 4; i++ {
		delivered += w
		w = p.next(now.Add(time.Duration(i)*time.Second), deliveryFeedback{Window: w, Flight: w, Demand: true, Delivered: delivered, RateMbps: 4, Age: 400 * time.Millisecond, Target: deliveryTargetAge, Samples: 8})
	}
	if w < deliveryInitialWindow {
		t.Fatal("self-limited measured rate prevents capacity discovery", w)
	}
	before := w
	w = p.next(now.Add(4*time.Second), deliveryFeedback{Window: w, Flight: w, Demand: true, Delivered: delivered + 65536, RateMbps: 2, Age: 2 * time.Second, FastAge: time.Second, Target: deliveryTargetAge, Pressure: true})
	if w >= before {
		t.Fatal("capacity probe bypassed genuine queue pressure")
	}
}

func TestQuietCarrierBurstBacklogLosesReserveBonus(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 10; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		g.attach(l)
	}
	g.lanes["a01"].unreceivedLocalRecords.Store(8)
	g.lanes["a02"].unreceivedLocalRecords.Store(8)
	g.mu.Lock()
	defer g.mu.Unlock()
	keys := []string{"a01", "a02", "a03"}
	g.prioritizeFastTargetsLocked(keys, nil)
	if keys[0] != "a03" {
		t.Fatal("cold setup queued behind unread short BODYs", keys)
	}
}

func TestBulkRateUsesCompleteFeedbackTurn(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.maintain(now, false)
	d.bulkDelivered, d.delivered = 1<<20, 1<<20
	d.maintain(now.Add(250*time.Millisecond), false)
	d.maintain(now.Add(500*time.Millisecond), false)
	d.maintain(now.Add(750*time.Millisecond), false)
	d.maintain(now.Add(time.Second), false)
	if d.bulkRateMbps < 8 || d.bulkRateMbps > 9 {
		t.Fatal("bursty useful-byte rate lost to an empty 250 ms sample", d.bulkRateMbps)
	}
}
