package tunnel

import (
	"context"
	"testing"
	"time"

	"itom.local/core/wire"
)

func TestRev2DrainedCongestionStartsFreshBoundedBulkEpoch(t *testing.T) {
	p := deliveryPolicy{mode: deliveryCongested, lastReduction: time.Now(), lastProbe: time.Now()}
	now := time.Now()
	w := p.next(now, deliveryFeedback{Window: deliveryMinWindow, Target: deliveryTargetAge})
	if w != deliveryInitialWindow {
		t.Fatalf("a drained previous test permanently throttles the next: window=%d want=%d", w, deliveryInitialWindow)
	}
	if !p.lastReduction.IsZero() || !p.lastProbe.IsZero() {
		t.Fatal("a previous epoch's congestion cooldown survived its drain")
	}
}

func TestRev2ColdMobileEpochRetainsItsBoundedSeed(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.observeProviderBaseline(300 * time.Millisecond)
	g.delivery.window = deliveryMinWindow
	g.delivery.maintain(time.Now(), false)
	if g.delivery.window != 256<<10 {
		t.Fatalf("fresh mobile probe lost its independently bounded seed: %d", g.delivery.window)
	}
}

func TestRev2BriefACKDrainIsNotACompletedBulkEpoch(t *testing.T) {
	d := newDeliveryController()
	d.window = deliveryMinWindow
	now := time.Now()
	d.maintain(now, true)
	for i := 1; i <= 3; i++ {
		d.maintain(now.Add(time.Duration(i)*250*time.Millisecond), false)
		if d.policy.mode == deliveryIdle || d.window != deliveryMinWindow {
			t.Fatal("a brief gap between active bulk batches reset the congestion probe", i, d.window, d.policy.mode)
		}
	}
	d.maintain(now.Add(2*time.Second), false)
	if d.policy.mode != deliveryIdle || d.window != deliveryInitialWindow {
		t.Fatal("a genuinely drained epoch did not restart its bounded probe")
	}
}

func TestRev2BulkCannotRetroactivelyBlameEarlierShortTraffic(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	short := deliveryJobs(t, 1, 1, 1024)
	if _, err := g.reserveDelivery(context.Background(), short); err != nil {
		t.Fatal(err)
	}
	bulk := deliveryJobs(t, 3, 1, wire.MaxData)
	if _, err := g.reserveDelivery(context.Background(), bulk); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, r := range g.delivery.ranges {
		if r.fast {
			r.admitted = now.Add(-2 * time.Second)
			if !r.isolatedFast {
				t.Fatal("new bulk retroactively reclassified an already admitted short exchange")
			}
		}
	}
	g.delivery.lastDecision = now.Add(-time.Second)
	before := g.delivery.window
	g.delivery.maintain(now, true)
	if g.delivery.window < before || g.delivery.fast != 1024 || g.delivery.bulk != wire.MaxData {
		t.Fatal("unrelated earlier delay throttled bulk or changed charged flight", g.delivery.window, g.delivery.fast, g.delivery.bulk)
	}
}

func TestRev2ActuallyBulkExposedShortDelayStillReducesAdmission(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	if _, err := g.reserveDelivery(context.Background(), deliveryJobs(t, 3, 1, wire.MaxData)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.reserveDelivery(context.Background(), deliveryJobs(t, 1, 1, 1024)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, r := range g.delivery.ranges {
		if r.fast {
			if r.isolatedFast {
				t.Fatal("actual bulk-exposed traffic was mislabeled")
			}
			r.admitted = now.Add(-time.Second)
		}
	}
	g.delivery.lastDecision = now.Add(-time.Second)
	before := g.delivery.window
	g.delivery.maintain(now, true)
	if g.delivery.window >= before {
		t.Fatal("actual loaded short-delay objective no longer protects admission")
	}
}

func TestRev2BatchedACKCannotHideFullUtilizationDuringRamp(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	f := deliveryFeedback{Window: deliveryInitialWindow, Flight: 0, ACKFlight: deliveryInitialWindow, Delivered: deliveryInitialWindow, Demand: true, Age: 200 * time.Millisecond, Target: deliveryTargetAge, Samples: 4}
	w := p.next(now, f)
	if w != deliveryInitialWindow+deliveryInitialWindow/2 {
		t.Fatal("ACK drain hid full utilization and caused additive ramp", w)
	}
	// Historical utilization cannot grow another turn without a useful ACK.
	f.Window = w
	f.ACKFlight = 0
	f.Samples = 0
	if got := p.next(now.Add(time.Second), f); got != w {
		t.Fatal("stale ACK authorization grew flight", got)
	}
	// The same authentic ACK must never override present queue pressure.
	f.Samples = 4
	f.ACKFlight = w
	f.Delivered += w
	f.FastAge = time.Second
	if got := p.next(now.Add(2*time.Second), f); got >= w {
		t.Fatal("ACK utilization bypassed loaded short queue guard", got)
	}
}
func TestRev2ACKUtilizationIsConsumedOnceAndRetirementCannotCreateIt(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	if _, e := g.reserveDelivery(context.Background(), deliveryJobs(t, 3, 1, wire.MaxData)); e != nil {
		t.Fatal(e)
	}
	d := &g.delivery
	now := time.Now()
	d.progress(3, wire.MaxData, now)
	if d.ackFlight == 0 {
		t.Fatal("authenticated ACK lost prior flight")
	}
	d.maintain(now.Add(time.Second), true)
	if d.ackFlight != 0 {
		t.Fatal("ACK flight authorization survived decision")
	}
	if _, e := g.reserveDelivery(context.Background(), deliveryJobs(t, 5, 1, wire.MaxData)); e != nil {
		t.Fatal(e)
	}
	d.retire(5)
	if d.ackFlight != 0 {
		t.Fatal("stream retirement manufactured utilization proof")
	}
}

func TestRev2ColdSeedIsIndependentOfAccountCalibrationArrivalOrder(t *testing.T) {
	for _, samples := range [][]time.Duration{{350 * time.Millisecond, 80 * time.Millisecond}, {80 * time.Millisecond, 350 * time.Millisecond}} {
		g := newStripeGroup(wire.ID{1}, 1)
		for _, sample := range samples {
			g.observeProviderBaseline(sample)
		}
		if g.delivery.baselineMS != 80 || g.delivery.initialWindow != deliveryInitialWindow || g.delivery.window != deliveryInitialWindow {
			t.Fatal("same cold paths chose different probe because first account was slower", samples, g.delivery.baselineMS, g.delivery.window)
		}
	}
	g := newStripeGroup(wire.ID{1}, 1)
	g.observeProviderBaseline(350 * time.Millisecond)
	g.delivery.bulkDelivered = 1
	g.delivery.delivered = 1
	g.observeProviderBaseline(80 * time.Millisecond)
	if g.delivery.window != 256<<10 || g.delivery.initialWindow != 256<<10 {
		t.Fatal("later calibration reset active learned delivery")
	}
}

func TestRev2RTTDiagnosticsDistinguishFreshAndHistoricalAccountSamples(t *testing.T) {
	now := time.Now()
	l := &lane{}
	l.rtt.Store(420)
	l.smoothedRTT.Store(350)
	l.idleRTT.Store(280)
	l.rttAt.Store(now.Add(-2 * time.Second).UnixMilli())
	var line LineDiagnostic
	applyRTTDiagnostics(&line, l, now)
	if !line.RTTFresh || line.RTTSampleAgeMS != 2000 || line.RTTMillis != 420 || line.RTTSmoothedMillis != 350 || line.RTTIdleMillis != 280 || line.RTTSampleKind != "CONTROL_ECHO" {
		t.Fatal(line)
	}
	applyRTTDiagnostics(&line, l, now.Add(20*time.Second))
	if line.RTTFresh || line.RTTMillis != 420 || line.RTTSampleAgeMS != 22000 {
		t.Fatal("stale RTT hidden or presented as a current sample", line)
	}
}
