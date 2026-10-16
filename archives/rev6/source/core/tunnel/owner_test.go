package tunnel

import (
	"context"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wire"
	"testing"
	"time"
)

func checkLogicalOwnerSurvivesCarrierEpochWithoutNewBudget(t *testing.T) {
	parent, stop := context.WithCancel(context.Background())
	defer stop()
	physical, closeCarrier := context.WithCancel(parent)
	logical, closeOwner := context.WithCancel(parent)
	defer closeOwner()
	g := newStripeGroup(wire.ID{4}, 6)
	owner := &lane{ctx: physical, cancel: closeCarrier, ownerCtx: logical, ownerCancel: closeOwner, account: servercfg.Account{ID: "a01"}, logicalSession: g.logical, generation: g.generation, carrierEpoch: 1, ownerStarted: true, actorReady: make(chan struct{}), ownerFrames: make(chan wire.Frame, 4)}
	close(owner.actorReady)
	if _, err := g.claimOwner(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	closeCarrier()
	g.detach(owner)
	if g.owner(1) != owner || g.activeStreams() != 1 || owner.logicalContext().Err() != nil {
		t.Fatal("carrier loss retired logical socket")
	}
	for epoch := uint64(2); epoch <= 100; epoch++ {
		carrier := &lane{ctx: parent, account: owner.account, logicalSession: g.logical, generation: g.generation, carrierEpoch: epoch}
		if err := g.attach(carrier); err != nil {
			t.Fatal(err)
		}
		got, err := g.claimOwner(carrier)
		if err != nil || got != owner || len(g.logicalOwners) != 1 {
			t.Fatal("rejoin allocated another logical budget", err)
		}
		g.detach(carrier)
	}
	stop()
	if owner.logicalContext().Err() == nil {
		t.Fatal("parent shutdown did not cancel retained owner")
	}
	g.releaseOwner(owner)
	if !g.empty() || g.activeStreams() != 0 {
		t.Fatal("logical shutdown retained routes")
	}
}

func checkStreamControlRoutesToDetachedOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := newStripeGroup(wire.ID{5}, 7)
	owner := &lane{ctx: ctx, ownerCtx: ctx, ownerFrames: make(chan wire.Frame, 8)}
	carrier := &lane{ctx: ctx}
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []wire.Type{wire.OpenOK, wire.Window, wire.Receipt, wire.Nack, wire.Fin} {
		f := wire.Frame{Type: typ, Stream: 1, Offset: 42}
		if err := g.dispatchStream(carrier, f); err != nil {
			t.Fatal(err)
		}
		got := <-owner.ownerFrames
		if got.Type != typ || got.Stream != 1 || got.Offset != 42 {
			t.Fatal("control delivered to wrong actor")
		}
	}
	if err := g.dispatchStream(carrier, wire.Frame{Type: wire.Window, Stream: 3}); err != errStripeStream {
		t.Fatal("unknown stream accepted", err)
	}
	g.retire(1, owner)
	if err := g.dispatchStream(carrier, wire.Frame{Type: wire.Window, Stream: 1}); err != nil {
		t.Fatal("late tombstone rejected", err)
	}
}

func checkOwnerWatchdogUsesAuthenticatedProgress(t *testing.T) {
	g := newStripeGroup(wire.ID{3}, 2)
	l := &lane{}
	now := time.Now()
	l.lastProgress.Store(now.Add(-26 * time.Second).UnixNano())
	g.logicalOwners["a01"] = l
	if g.progressAge(now) <= transportProgressLimit {
		t.Fatal("stale detached owner bypassed watchdog")
	}
	sibling := &lane{}
	sibling.lastProgress.Store(now.Add(-time.Second).UnixNano())
	g.lanes["a02"] = sibling
	if g.progressAge(now) > 2*time.Second {
		t.Fatal("fresh authenticated sibling progress ignored")
	}
	// READY and local queue activity alone never refresh remote progress.
	sibling.ready.Store(true)
	delete(g.lanes, "a02")
	if g.progressAge(now) <= transportProgressLimit {
		t.Fatal("local readiness refreshed remote watchdog")
	}
}

func checkLogicalControlCapabilityRejectsField2Peer(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	user := servercfg.User{ID: "12345678", Up: "itom-up", Down: "itom-down"}
	server := wire.ID{1}
	clientNonce := wire.ID{2}
	oldCaps := transportCapabilities &^ capLogicalControl
	p := preauthProbe{Version: transportWireVersion, Capabilities: oldCaps, ClientNonce: clientNonce}
	p.MAC = preauthMAC(key, "probe", server, user, "a01", clientNonce, wire.ID{}, oldCaps)
	if validProbe(key, server, user, "a01", p) {
		t.Fatal("field2 peer accepted new logical control semantics")
	}
	p.Capabilities = transportCapabilities
	p.MAC = preauthMAC(key, "probe", server, user, "a01", clientNonce, wire.ID{}, p.Capabilities)
	if !validProbe(key, server, user, "a01", p) {
		t.Fatal("matching capability rejected")
	}
}

func checkReplacementNeedsFreshEchoBeforeStreamAdmission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := newStripeGroup(wire.ID{4}, 3)
	owner := stripeTestLane(g, "a01", true)
	defer owner.cancel()
	owner.actorReady = make(chan struct{})
	close(owner.actorReady)
	owner.ownerCancel = owner.cancel
	if _, err := g.claimOwner(owner); err != nil {
		t.Fatal(err)
	}
	defer g.releaseOwner(owner)
	fresh := stripeTestLane(g, "a01", true)
	fresh.ownerCtx = ctx
	defer fresh.cancel()
	fresh.carrierEpoch = 2
	fresh.controlQueue = make(chan queuedControl, 4)
	if err := g.attach(fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.streamCarrierReady() || fresh.openHealthy(time.Now()) {
		t.Fatal("READY bypassed fresh rejoin echo")
	}
	if err := fresh.queueRejoinProbe(); err != nil {
		t.Fatal(err)
	}
	req := <-fresh.controlQueue
	c, err := wire.DecodeControl(req.Frame)
	if err != nil {
		t.Fatal(err)
	}
	m := c.(wire.Echo)
	// A request from the peer and a stale reply cannot confirm our own round trip.
	fresh.completeEchoReply(m.Token + 1)
	if !fresh.rejoinPending.Load() {
		t.Fatal("stale echo confirmed readiness")
	}
	fresh.completeEchoReply(m.Token)
	if !fresh.streamCarrierReady() {
		t.Fatal("fresh matching echo did not enable stream traffic")
	}
}

func TestStreamDispatchCannotBypassCarrierActivation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := newStripeGroup(wire.ID{5}, 2)
	owner := &lane{ctx: ctx, ownerFrames: make(chan wire.Frame, 1)}
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	gate, _, err := session.NewGate(session.Client, 1, 0, transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	carrier := &lane{ctx: ctx, stripe: g, gate: gate}
	f, err := wire.EncodeControl(1, wire.Credit{MaxStream: 10, MaxData: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err = carrier.process(f); err == nil || err.Error() != "SESSION_NOT_ACTIVATED" {
		t.Fatal("unactivated carrier delivered stream control", err)
	}
	select {
	case <-owner.ownerFrames:
		t.Fatal("unactivated stream frame entered logical actor")
	default:
	}
}

func checkLogicalControlAvoidsCongestedPreferredCarrier(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	a.controlQueue = make(chan queuedControl, 16)
	b.controlQueue = make(chan queuedControl, 16)
	g.attach(a)
	g.attach(b)
	for i := 0; i < 10; i++ {
		a.controlQueue <- queuedControl{}
	}
	frame, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 12345, MaxData: 12345})
	if err := g.sendStreamControl(context.Background(), a, frame); err != nil {
		t.Fatal(err)
	}
	if len(a.controlQueue) != 10 || len(b.controlQueue) != 1 {
		t.Fatal("logical control stayed behind congested preferred carrier")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioOwner(t *testing.T) {
	t.Log("check=checkLogicalOwnerSurvivesCarrierEpochWithoutNewBudget")
	checkLogicalOwnerSurvivesCarrierEpochWithoutNewBudget(t)
	t.Log("check=checkStreamControlRoutesToDetachedOwner")
	checkStreamControlRoutesToDetachedOwner(t)
	t.Log("check=checkOwnerWatchdogUsesAuthenticatedProgress")
	checkOwnerWatchdogUsesAuthenticatedProgress(t)
	t.Log("check=checkLogicalControlCapabilityRejectsField2Peer")
	checkLogicalControlCapabilityRejectsField2Peer(t)
	t.Log("check=checkReplacementNeedsFreshEchoBeforeStreamAdmission")
	checkReplacementNeedsFreshEchoBeforeStreamAdmission(t)
	t.Log("check=checkLogicalControlAvoidsCongestedPreferredCarrier")
	checkLogicalControlAvoidsCongestedPreferredCarrier(t)
}
