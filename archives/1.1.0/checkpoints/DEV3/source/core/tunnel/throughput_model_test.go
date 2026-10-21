package tunnel

import (
	"math"
	"testing"
	"time"
)

// These trace tests exercise decisions; the supervisor comparisons measure
// actual delivery separately. A trace test is not a bandwidth measurement.
func TestThroughputModelJitterDoesNotCollapseBulk(t *testing.T) {
	var m throughputModel
	now := time.Unix(100, 0)
	window := uint64(deliveryInitialWindow)
	for i := 0; i < 48; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		jitter := []time.Duration{40, 180, 80, 140, 45, 120}[i%6] * time.Millisecond
		window = m.next(at, modelFeedback{deliveryFeedback: deliveryFeedback{
			Window: window, Flight: window, ACKFlight: window, Demand: true,
			Delivered: uint64(i+1) * (12 << 20), RateMbps: 100,
			Age: 3 * time.Second, FastAge: time.Second, Pressure: true,
			Target: 800 * time.Millisecond, FastTarget: 650 * time.Millisecond,
		}, RateAt: at, RTTAt: at, RTT: 4 * jitter})
		if window < deliveryInitialWindow || window > deliveryMaxWindow {
			t.Fatal("jitter escaped bounded bulk admission", i, window)
		}
	}
	if window < 4<<20 || m.bandwidth != 100 || m.mode == deliveryCongested {
		t.Fatal("constant useful delivery collapsed on RTT alone", window, m)
	}
}

func TestThroughputModelNoProgressCannotGrowAndStallIsBounded(t *testing.T) {
	var m throughputModel
	now := time.Unix(100, 0)
	f := modelFeedback{deliveryFeedback: deliveryFeedback{Window: 8 << 20, Flight: 8 << 20, Demand: true}}
	before := f.Window
	for i := 0; i < 13; i++ {
		f.Window = m.next(now.Add(time.Duration(i)*250*time.Millisecond), f)
		if f.Window > before {
			t.Fatal("empty ticks grew admission")
		}
		before = f.Window
	}
	if f.Window >= 8<<20 || m.mode != deliveryOutage {
		t.Fatal("collective stall remained unbounded", f.Window, m.mode)
	}
	for i := 4; i <= 20; i++ {
		f.Window = m.next(now.Add(time.Duration(i)*time.Second), f)
	}
	if f.Window != deliveryMinWindow {
		t.Fatal("persistent stall failed to reach floor", f.Window)
	}
	f.Delivered = 1
	f.ACKFlight = f.Window
	f.Window = m.next(now.Add(21*time.Second), f)
	if f.Window <= deliveryMinWindow {
		t.Fatal("fresh progress failed to recover")
	}
}

func TestThroughputModelRateFreshnessAndAppLimitedSamples(t *testing.T) {
	var m throughputModel
	now := time.Unix(100, 0)
	f := modelFeedback{deliveryFeedback: deliveryFeedback{Window: 4 << 20, Flight: 4 << 20, ACKFlight: 4 << 20, Demand: true, Delivered: 1, RateMbps: 100}, RateAt: now}
	m.next(now, f)
	f.RateMbps = 1
	f.Flight = 1
	f.ACKFlight = 1
	for i := 1; i < 20; i++ {
		f.Delivered++
		f.RateAt = now.Add(time.Duration(i) * time.Second)
		m.next(f.RateAt, f)
	}
	if m.bandwidth != 100 || m.lowRates != 0 {
		t.Fatal("app-limited sample erased capacity", m)
	}
	f.Flight, f.ACKFlight = 4<<20, 4<<20
	f.RateAt = now.Add(21 * time.Second)
	m.next(f.RateAt, f)
	for i := 0; i < 10; i++ {
		m.next(f.RateAt.Add(time.Duration(i)*time.Millisecond), f)
	}
	if m.lowRates != 1 {
		t.Fatal("same rate sample was counted on empty actor ticks", m.lowRates)
	}
}

func TestThroughputModelCapacityDropAndRecovery(t *testing.T) {
	var m throughputModel
	now := time.Unix(100, 0)
	f := modelFeedback{deliveryFeedback: deliveryFeedback{Window: 16 << 20, Flight: 16 << 20, ACKFlight: 16 << 20, Demand: true, RateMbps: 100}, RTT: 200 * time.Millisecond}
	for i := 0; i < 4; i++ {
		f.Delivered++
		f.RateAt = now.Add(time.Duration(i) * time.Second)
		f.RTTAt = f.RateAt
		m.next(f.RateAt, f)
	}
	f.RateMbps = 10
	for i := 4; i < 7; i++ {
		f.Delivered++
		f.RateAt = now.Add(time.Duration(i) * time.Second)
		f.RTTAt = f.RateAt
		f.Window = m.next(f.RateAt, f)
	}
	if m.bandwidth != 10 || f.Window >= 16<<20 {
		t.Fatal("sustained full-window delivery drop ignored", f.Window, m)
	}
	low := f.Window
	f.RateMbps = 150
	f.Delivered++
	f.RateAt = now.Add(8 * time.Second)
	f.RTTAt = f.RateAt
	f.Window = m.next(f.RateAt, f)
	if m.bandwidth != 150 || f.Window <= low {
		t.Fatal("higher useful delivery could not recover bandwidth", f.Window, m)
	}
}

func TestThroughputModelDrainedEpochKeepsCapacityNotFlight(t *testing.T) {
	m := throughputModel{bandwidth: 100}
	now := time.Unix(100, 0)
	f := modelFeedback{deliveryFeedback: deliveryFeedback{Window: 16 << 20, InitialWindow: deliveryInitialWindow}}
	for i := 0; i < 30; i++ {
		if got := m.next(now.Add(time.Duration(i)*time.Second), f); got != deliveryInitialWindow {
			t.Fatal("idle kept bulk flight", got)
		}
	}
	if m.bandwidth != 100 || m.mode != deliveryIdle {
		t.Fatal("idle erased capacity or started bulk", m)
	}
}

func TestThroughputModelRejectsInvalidRateAndBoundsFlight(t *testing.T) {
	for _, rate := range []float64{math.NaN(), math.Inf(1), -1, 10001} {
		var m throughputModel
		got := m.next(time.Unix(100, 0), modelFeedback{deliveryFeedback: deliveryFeedback{Window: math.MaxUint64, Flight: math.MaxUint64, Demand: true, Delivered: 1, RateMbps: rate}, RateAt: time.Unix(100, 0)})
		if got > deliveryMaxWindow || m.bandwidth != 0 {
			t.Fatal("invalid rate expanded authority", rate, got, m.bandwidth)
		}
	}
}

func TestThroughputModelPlateauDoesNotGrowUnboundedQueue(t *testing.T) {
	var m throughputModel
	now := time.Unix(100, 0)
	window := uint64(deliveryInitialWindow)
	for i := 0; i < 60; i++ {
		at := now.Add(time.Duration(i) * time.Second)
		window = m.next(at, modelFeedback{deliveryFeedback: deliveryFeedback{Window: window, Flight: window, ACKFlight: window, Demand: true, Delivered: uint64(i+1) * (12 << 20), RateMbps: 100}, RateAt: at, RTTAt: at, RTT: 200 * time.Millisecond})
		if i > 10 && window > uint64((100e6/8*.2*2+128*1024)*1.25) {
			t.Fatal("plateau built unbounded queue", window)
		}
	}
}

func TestThroughputModelPublishedSupplyCannotEraseNetworkCapacity(t *testing.T) {
	m := throughputModel{retainWindow: true}
	now := time.Unix(100, 0)
	f := modelFeedback{deliveryFeedback: deliveryFeedback{Window: 16 << 20, Flight: 16 << 20, ACKFlight: 16 << 20, Demand: true, Delivered: 1, RateMbps: 100}, RateAt: now, RTT: 200 * time.Millisecond, RTTAt: now}
	m.next(now, f)
	f.SupplyLimited = true
	f.RateMbps = 10
	for i := 1; i < 10; i++ {
		f.Delivered++
		f.RateAt = now.Add(time.Duration(i) * time.Second)
		f.RTTAt = f.RateAt
		f.Window = m.next(f.RateAt, f)
	}
	if m.bandwidth != 100 || f.Window != 16<<20 || m.lowRates != 0 {
		t.Fatal("physical supply was misclassified as network congestion", m, f.Window)
	}
	f.SupplyLimited = false
	for i := 10; i < 13; i++ {
		f.Delivered++
		f.RateAt = now.Add(time.Duration(i) * time.Second)
		f.RTTAt = f.RateAt
		f.Window = m.next(f.RateAt, f)
	}
	if m.bandwidth != 10 || f.Window >= 16<<20 {
		t.Fatal("high-water hid a confirmed published-flight capacity drop", m, f.Window)
	}
}

func TestThroughputModelHighWaterDoesNotSurviveDrainedBulk(t *testing.T) {
	m := throughputModel{retainWindow: true}
	now := time.Unix(100, 0)
	got := m.next(now, modelFeedback{deliveryFeedback: deliveryFeedback{Window: 16 << 20}})
	if got != deliveryInitialWindow || m.mode != deliveryIdle {
		t.Fatal("bulk high-water leaked into the quiet phase", got, m.mode)
	}
}

func TestThroughputModelSupplyWaitExcludesAdmittedAPPENDs(t *testing.T) {
	d := newDeliveryController()
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 1 << 20, carrier: "a01"}
	d.ranges[r.key] = r
	d.bulk = 1 << 20
	if d.modelSupplyLimited() {
		t.Fatal("unfinished but physically admitted APPEND was labeled quota-limited")
	}
	r.carrier = ""
	r.published = true
	if d.modelSupplyLimited() {
		t.Fatal("unknown published debt was labeled unstarted quota wait")
	}
	r.published = false
	if !d.modelSupplyLimited() {
		t.Fatal("range awaiting grant was missed")
	}
	r.carrier = "a01"
	fast := &deliveryRange{key: deliveryKey{3, 0}, end: 128 << 10, fast: true}
	d.ranges[fast.key] = fast
	if d.modelSupplyLimited() {
		t.Fatal("short reservation became bulk supply pressure")
	}
}
