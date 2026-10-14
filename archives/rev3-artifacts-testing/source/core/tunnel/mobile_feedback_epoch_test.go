package tunnel

import (
	"context"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestUnloadedMobileACKCannotThrottleNextBulkBurst(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.observeProviderBaseline(102 * time.Millisecond)
	jobs := deliveryJobs(t, 1, 1, 1024)
	reservation, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	reservation.finish(jobs, committed(jobs, flow.Committed))
	now := time.Now()
	for _, r := range g.delivery.ranges {
		r.admitted = now
	}
	g.delivery.progress(1, 1024, now.Add(900*time.Millisecond))
	initial := g.delivery.window
	g.delivery.maintain(now.Add(time.Second), true)
	if g.delivery.window < initial {
		t.Fatal("unloaded ACK shrank the next bulk burst", initial, g.delivery.window)
	}
	if g.delivery.delivered != 1024 || g.delivery.bulk != 0 || g.delivery.fast != 0 {
		t.Fatal("baseline calibration changed debt")
	}
}

func TestOneSlowACKEpisodeCannotHalveOnEveryActorTick(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	window := uint64(deliveryInitialWindow)
	var first uint64
	for i := 0; i < 4; i++ {
		window = p.next(now.Add(time.Duration(i)*250*time.Millisecond), deliveryFeedback{
			Window: window, Flight: 32 << 10, Demand: true, Delivered: uint64(i+1) * 65536,
			RateMbps: 2, Age: 500 * time.Millisecond, FastAge: 900 * time.Millisecond,
			Target: deliveryTargetAge, Pressure: true,
		})
		if i == 0 {
			first = window
		}
		if window != first {
			t.Fatal("one feedback episode exhausted the window", i, window)
		}
	}
	window = p.next(now.Add(2*time.Second), deliveryFeedback{Window: window, Flight: 32 << 10, Demand: true,
		Delivered: 5 * 65536, Age: 500 * time.Millisecond, FastAge: 2 * time.Second, Target: deliveryTargetAge, Pressure: true})
	if window != max(uint64(deliveryMinWindow), first/2) {
		t.Fatal("persistent congestion was not reduced again", window)
	}
}

func TestUnloadedFastFloorIsBoundedAndNotLearnedFromBulkOrRepair(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	for i := 0; i < 8; i++ {
		id := uint64(i*2 + 1)
		r := &deliveryRange{key: deliveryKey{id, 0}, end: 1024, fast: true, unloadedFast: true, admitted: now}
		d.ranges[r.key] = r
		d.fast += 1024
		d.progress(id, 1024, now.Add(time.Duration(700+i*30)*time.Millisecond))
	}
	if d.fastPathBaseline() < 800*time.Millisecond || d.fastAgeBudget() != deliveryFastMaxAge {
		t.Fatal("normal mobile ACK path not calibrated", d.fastPathBaseline(), d.fastAgeBudget())
	}
	count := d.fastBaselineCount
	r := &deliveryRange{key: deliveryKey{101, 0}, end: 1024, fast: true, unloadedFast: true, admitted: now}
	d.ranges[r.key] = r
	d.fast = 1024
	d.invalidateRepairFloor(101, 0, 1024)
	d.progress(101, 1024, now.Add(5*time.Second))
	if d.fastBaselineCount != count {
		t.Fatal("repaired ACK inflated baseline")
	}
	d.baselineMS = 10000
	if d.fastAgeBudget() > 3*time.Second || d.targetAge() > 3*time.Second {
		t.Fatal("queue guard is unbounded")
	}
}

func TestNaturalFastPathCannotTripFractionOfTotalRTTGuard(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	d.baselineMS = 100
	d.fastBaselineCount = 16
	for i := range d.fastBaselineAges {
		d.fastBaselineAges[i] = 800 * time.Millisecond
	}
	d.bulkDelivered = 65536
	d.delivered = 65536
	d.bulk = 65536
	d.ageCount = 4
	for i := 0; i < 4; i++ {
		d.ages[i] = 1050 * time.Millisecond
	}
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 1024, fast: true, admitted: now.Add(-800 * time.Millisecond)}
	d.ranges[r.key] = r
	d.fast = 1024
	d.maintain(now, true)
	if d.window >= deliveryInitialWindow {
		t.Fatal("late path was exempted from the fixed latency objective", d.window, d.fastPathBaseline(), d.fastAgeBudget())
	}
	if d.fast != 1024 || d.bulk != 65536 || d.delivered != 65536 {
		t.Fatal("queue calibration changed debt")
	}
}

func TestLateUsefulBulkCannotExemptFixedLatencyObjective(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	window := p.next(now, deliveryFeedback{Window: deliveryInitialWindow, Flight: 32 << 10, Demand: true,
		Delivered: 65536, RateMbps: 2, Age: 1250 * time.Millisecond, Samples: 4, Target: deliveryTargetAge})
	if window >= deliveryInitialWindow || window < deliveryMinWindow {
		t.Fatal("late useful delivery did not bound new flight", window)
	}
	window = p.next(now.Add(time.Second), deliveryFeedback{Window: window, Flight: 32 << 10, Demand: true,
		Delivered: 65536, Age: 2250 * time.Millisecond, Target: deliveryTargetAge})
	if window >= deliveryInitialWindow {
		t.Fatal("actual lack of progress did not reduce new flight", window)
	}
}

func TestFastBaselineQualificationEndsWhenBulkSharesPath(t *testing.T) {
	g := newStripeGroup(wire.ID{2}, 1)
	fast := deliveryJobs(t, 1, 1, 1024)
	r, err := g.reserveDelivery(context.Background(), fast)
	if err != nil {
		t.Fatal(err)
	}
	r.finish(fast, committed(fast, flow.Committed))
	bulk := deliveryJobs(t, 3, 1, 32<<10)
	r, err = g.reserveDelivery(context.Background(), bulk)
	if err != nil {
		t.Fatal(err)
	}
	r.finish(bulk, committed(bulk, flow.Committed))
	now := time.Now()
	for _, pending := range g.delivery.ranges {
		pending.admitted = now
	}
	g.delivery.progress(1, 1024, now.Add(time.Second))
	if g.delivery.fastBaselineCount != 0 {
		t.Fatal("bulk-exposed ACK inflated an unloaded baseline")
	}
	if g.delivery.bulk != 32<<10 || g.delivery.delivered != 1024 {
		t.Fatal("baseline qualification refunded bulk debt")
	}
}
