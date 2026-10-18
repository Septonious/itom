package tunnel

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/flow"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestInteractiveReserveAvoidsSlowIdleAccounts(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 6; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.rtt.Store(350)
		l.rttAt.Store(time.Now().UnixMilli())
		if i <= 2 {
			l.rtt.Store(1400)
		}
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	reserve := g.interactiveReadersLocked()
	g.mu.Unlock()
	if reserve[0].account.ID != "a03" || reserve[1].account.ID != "a04" {
		t.Fatal("account names overrode measured idle latency", reserve[0].account.ID, reserve[1].account.ID)
	}
	g.lanes["a03"].controlDebt.Store(90)
	g.mu.Lock()
	next := g.interactiveReadersLocked()
	g.mu.Unlock()
	if next != reserve {
		t.Fatal("temporary debt rotated the quiet reserve")
	}
}

func TestBulkPlacementTracksUnreadClientBytesAfterAppend(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for _, id := range []string{"a01", "a02"} {
		l := stripeTestLane(g, id, false)
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	j := deliveryJobs(t, 1, 2, 32<<10)
	r, err := g.reserveDelivery(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	g.bindDeliveryCarrier(j, "a01")
	r.finish(j, committed(j, flow.Committed))
	g.mu.Lock()
	keys := []string{"a01", "a02"}
	g.prioritizeBulkTargetsLocked(keys)
	g.mu.Unlock()
	if keys[0] != "a02" {
		t.Fatal("APPEND hid the unread client publication", keys)
	}
	g.deliveryProgress(1, 32<<10)
	g.mu.Lock()
	keys = []string{"a01", "a02"}
	g.prioritizeBulkTargetsLocked(keys)
	g.mu.Unlock()
	if keys[0] != "a02" {
		t.Fatal("partial logical ACK erased pending placement")
	}
	g.deliveryProgress(1, 64<<10)
	g.mu.Lock()
	keys = []string{"a01", "a02"}
	g.prioritizeBulkTargetsLocked(keys)
	g.mu.Unlock()
	if keys[0] != "a01" || g.delivery.delivered != 64<<10 {
		t.Fatal("completed ACK did not restore carrier", keys)
	}
}

func TestShortRepliesKeepIncomingPhysicalAffinity(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), true)
		defer l.cancel()
		l.controlQueue = make(chan queuedControl, 8)
		l.urgentQueue = make(chan queuedControl, 8)
		l.maintenanceQueue = make(chan queuedControl, 8)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	owner, carrier := g.lanes["a01"], g.lanes["a04"]
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	if err := g.dispatchStream(carrier, wire.Frame{Type: wire.Data, Stream: 1, Payload: make([]byte, 80)}); err != nil {
		t.Fatal(err)
	}
	f, _ := wire.EncodeControl(1, wire.Credit{MaxStream: runtimeStreamWindow, MaxData: runtimeReceiveBytes})
	if err := g.sendStreamControl(context.Background(), owner, f); err != nil {
		t.Fatal(err)
	}
	if len(carrier.urgentQueue) != 1 {
		t.Fatal("reply hopped off the request reader")
	}
	carrier.physicalSlots <- struct{}{}
	f.Offset++
	if err := g.sendStreamControl(context.Background(), owner, f); err != nil {
		t.Fatal(err)
	}
	if len(carrier.controlQueue) != 0 {
		t.Fatal("affinity pinned credit behind bulk")
	}
}

func TestColdBulkCapacityProbeDoesNotWaitForOneSecondEveryStep(t *testing.T) {
	p := deliveryPolicy{}
	now := time.Now()
	w := uint64(deliveryInitialWindow)
	for i := 0; i < 7; i++ {
		w = p.next(now.Add(time.Duration(i)*400*time.Millisecond), deliveryFeedback{Window: w, Flight: w, Demand: true,
			Delivered: uint64(i+1) << 20, RateMbps: 20, Age: 400 * time.Millisecond, Target: deliveryTargetAge, Samples: 8})
	}
	if w < 4<<20 {
		t.Fatal("healthy acknowledged flight ramps too slowly for short phases", w)
	}
	before := w
	w = p.next(now.Add(3*time.Second), deliveryFeedback{Window: w, Flight: w, Demand: true,
		Delivered: 8 << 20, RateMbps: 20, Age: 2 * time.Second, FastAge: time.Second, Target: deliveryTargetAge, Pressure: true})
	if w >= before {
		t.Fatal("faster discovery bypassed congestion")
	}
}

func TestCarrierFlightPartialACKRepairAndRetirement(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	j := deliveryJobs(t, 1, 2, 32<<10)
	r, err := g.reserveDelivery(context.Background(), j)
	if err != nil {
		t.Fatal(err)
	}
	g.bindDeliveryCarrier(j, "a01")
	g.bindDeliveryCarrier(j, "a01")
	if g.delivery.carrierBytes["a01"] != 64<<10 {
		t.Fatal("binding doubled carrier debt")
	}
	g.delivery.progress(1, 16<<10, time.Now())
	if g.delivery.carrierBytes["a01"] != 48<<10 {
		t.Fatal("partial ACK lost placement bytes")
	}
	g.bindDeliveryCarrier(j, "a02")
	if g.delivery.carrierBytes["a01"] != 0 || g.delivery.carrierBytes["a02"] != 48<<10 {
		t.Fatal("repair placement counted bytes twice")
	}
	r.finish(j, committed(j, flow.CommitUnknown))
	g.delivery.retire(1)
	if len(g.delivery.carrierBytes) != 0 || len(g.delivery.carrierACK) != 0 || g.delivery.delivered != 16<<10 {
		t.Fatal("unknown publication/retirement fabricated delivery or retained placement", g.delivery.carrierBytes)
	}
}

func TestShortAffinityAcrossDifferentEndpointReserves(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	keys := []string{"a01", "a02", "a03", "a04", "a05"}
	g.prioritizeFastTargetsLocked(keys, nil)
	g.preferShortTargetLocked(keys, g.lanes["a04"])
	if keys[0] != "a04" {
		t.Fatal("local reserve defeated the peer's primed physical return path", keys)
	}
	g.lanes["a04"].physicalSlots <- struct{}{}
	g.prioritizeFastTargetsLocked(keys, nil)
	g.preferShortTargetLocked(keys, g.lanes["a04"])
	if keys[0] == "a04" {
		t.Fatal("affinity defeated physical congestion")
	}
}

func TestInteractiveReserveDoesNotMoveIntoPublishedBulk(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.rtt.Store(350)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	g.mu.Lock()
	first := g.interactiveReadersLocked()
	g.mu.Unlock()
	g.delivery.bulk = 64 << 10
	g.lanes["a01"].rtt.Store(1200)
	g.mu.Lock()
	during := g.interactiveReadersLocked()
	g.mu.Unlock()
	if during != first {
		t.Fatal("loaded ping moved the reserve into an existing BODY queue")
	}
	g.delivery.bulk = 0
	g.mu.Lock()
	idle := g.interactiveReadersLocked()
	g.mu.Unlock()
	if idle == first {
		t.Fatal("reserve could not adapt between bulk epochs")
	}
}

func TestParallelShortBurstWaitsForResponsiveSlotInsteadOfSlowOutlier(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 6; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.rtt.Store(350)
		l.lastProgress.Store(time.Now().UnixNano())
		if i <= 2 {
			l.rtt.Store(1400)
		} else {
			l.physicalSlots <- struct{}{}
		}
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	l, _, err := g.takeRemoteGrantForClass(ctx, nil, true)
	if l != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("burst spilled onto a path four times slower instead of awaiting responsive capacity", l, err)
	}
	if len(g.lanes["a01"].fastGrants) != 1 || len(g.lanes["a02"].fastGrants) != 1 {
		t.Fatal("waiting consumed an unused slow grant")
	}
	<-g.lanes["a03"].physicalSlots
	g.signal()
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	l, _, err = g.takeRemoteGrantForClass(ctx2, nil, true)
	if err != nil || l != g.lanes["a03"] {
		t.Fatal("responsive path did not resume on capacity notification", l, err)
	}
}

func TestLoadedEchoCannotRaiseIdleCarrierSelectionBaseline(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	l.echo = 1
	l.echoIdle = true
	l.lastEcho = time.Now().Add(-250 * time.Millisecond)
	l.completeEchoReply(1)
	g.delivery.bulk = 32 << 10
	l.echo = 2
	l.lastEcho = time.Now().Add(-2 * time.Second)
	l.completeEchoReply(2)
	g.mu.Lock()
	latency := g.carrierLatencyLocked(l)
	g.mu.Unlock()
	if latency < 250 || latency > 275 {
		t.Fatal("loaded echo replaced the unloaded path cost", latency)
	}
	if l.rtt.Load() < 2000 {
		t.Fatal("diagnostics concealed actual loaded latency")
	}
}

type isolatedInitialControlMail struct{ mailbox.Transport }

func (isolatedInitialControlMail) DataMaintenanceIsolated() bool { return true }

func TestInitialReceiveCreditUsesProvenOpenResponsePath(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 1; i <= 5; i++ {
		l := stripeTestLane(g, fmt.Sprintf("a%02d", i), false)
		defer l.cancel()
		l.controlQueue = make(chan queuedControl, 8)
		l.urgentQueue = make(chan queuedControl, 8)
		l.maintenanceQueue = make(chan queuedControl, 8)
		l.mail = isolatedInitialControlMail{}
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	owner, carrier := g.lanes["a01"], g.lanes["a04"]
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	openOK, _ := wire.EncodeControl(1, wire.ConnectResult{Code: wire.ReasonNone})
	if err := g.dispatchStream(carrier, openOK); err != nil {
		t.Fatal(err)
	}
	carrier.physicalSlots <- struct{}{}
	f, _ := wire.EncodeControl(1, wire.Credit{MaxStream: runtimeStreamWindow, MaxData: runtimeReceiveBytes})
	if err := g.sendStreamControl(context.Background(), owner, f); err != nil {
		t.Fatal(err)
	}
	if len(carrier.urgentQueue) != 1 {
		t.Fatal("independent short DATA socket moved initial credit off the proven OPEN path")
	}
}

func TestLoadedOnlyCarriersUseUnloadedPoolEstimate(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for _, id := range []string{"a01", "a02", "a03"} {
		l := stripeTestLane(g, id, false)
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	idle := g.lanes["a01"]
	idle.idleRTT.Store(350)
	idle.idleRTTAt.Store(time.Now().UnixMilli())
	busy := g.lanes["a02"]
	busy.rtt.Store(2000)
	busy.rttAt.Store(time.Now().UnixMilli())
	g.active.Store(2)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.carrierLatencyLocked(busy) != 350 || g.carrierLatencyLocked(g.lanes["a03"]) != 350 {
		t.Fatal("loaded-only RTT treated as unloaded path propagation")
	}
}
