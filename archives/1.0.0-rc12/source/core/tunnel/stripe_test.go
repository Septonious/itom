package tunnel

import (
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func stripeTestLane(g *stripeGroup, account string, side bool) *lane {
	ctx, cancel := context.WithCancel(context.Background())
	l := &lane{ctx: ctx, cancel: cancel, side: side, account: servercfg.Account{ID: account}, logicalSession: g.logical, generation: g.generation, carrierEpoch: 1, inputs: make(chan datapath.Input, 8), opens: map[uint64]*opening{}}
	l.physicalSlots = make(chan struct{}, 1)
	l.fastGrants = make(chan wire.QuotaKey, 1)
	l.grants = make(chan wire.QuotaKey, 1)
	l.fastGrants <- wire.QuotaKey{Account: account, User: "12345678", Store: wire.ID{1}, Session: g.logical, Epoch: 1, Grant: 1}
	l.ready.Store(true)
	return l
}

type stripeCapacityMail struct {
	mailbox.Transport
	parallel int
}

func (m *stripeCapacityMail) DataParallelism() int { return m.parallel }
func (m *stripeCapacityMail) DataAvailable() bool  { return m.parallel > 0 }

func checkStripeUsesLogicalSessionWideStreamIDs(t *testing.T) {
	g := newStripeGroup(wire.ID{9}, 7)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	if err := g.attach(a); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(b); err != nil {
		t.Fatal(err)
	}
	one, err := g.allocate(a)
	if err != nil {
		t.Fatal(err)
	}
	two, err := g.allocate(b)
	if err != nil {
		t.Fatal(err)
	}
	if one != 1 || two != 3 || g.owner(one) != a || g.owner(two) != b || g.activeStreams() != 2 {
		t.Fatalf("ids/owners one=%d two=%d active=%d", one, two, g.activeStreams())
	}
	g.retire(one, a)
	if g.owner(one) != nil || g.activeStreams() != 1 {
		t.Fatal("stream owner was not retired")
	}
	if owner, known := g.route(one); !known || owner != nil {
		t.Fatal("retired stream did not leave a bounded tombstone")
	}
	if _, known := g.route(5); known {
		t.Fatal("never-allocated stream was accepted as a tombstone")
	}
}

func checkStripePhysicalSlotsSpreadAcrossCarriers(t *testing.T) {
	g := newStripeGroup(wire.ID{8}, 3)
	lanes := []*lane{stripeTestLane(g, "a01", false), stripeTestLane(g, "a02", false), stripeTestLane(g, "a03", false), stripeTestLane(g, "a04", false)}
	for _, l := range lanes {
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	leases := make([]stripeLease, 0, len(lanes))
	seen := map[string]bool{}
	for range lanes {
		lease, err := g.acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
		seen[lease.lane.account.ID] = true
	}
	if len(seen) != len(lanes) {
		t.Fatalf("physical scheduler did not spread: %v", seen)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := g.acquire(ctx); err == nil {
		t.Fatal("slot bound was bypassed")
	}
	leases[0].release()
	lease, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	for _, x := range leases[1:] {
		x.release()
	}
}

func checkStripePrefersNonOwnerCarrierForDATA(t *testing.T) {
	g := newStripeGroup(wire.ID{4}, 2)
	owner := stripeTestLane(g, "a01", false)
	other := stripeTestLane(g, "a02", false)
	defer owner.cancel()
	defer other.cancel()
	if err := g.attach(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(other); err != nil {
		t.Fatal(err)
	}
	lease, err := g.acquireFor(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if lease.lane != other {
		t.Fatalf("owner DATA carrier was selected while alternate was healthy: got=%s", lease.lane.account.ID)
	}
}

func checkStripedDATAIsDeliveredToOwnerPump(t *testing.T) {
	g := newStripeGroup(wire.ID{7}, 5)
	owner := stripeTestLane(g, "a01", true)
	carrier := stripeTestLane(g, "a02", true)
	defer owner.cancel()
	defer carrier.cancel()
	if err := g.attach(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(carrier); err != nil {
		t.Fatal(err)
	}
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	f := wire.Frame{Type: wire.Data, Stream: 1, Offset: 0, Logical: 4, Payload: []byte("test")}
	if err := carrier.deliver(f); err != nil {
		t.Fatal(err)
	}
	select {
	case in := <-owner.inputs:
		if in.Frame == nil || in.Frame.Stream != 1 || string(in.Frame.Payload) != "test" {
			t.Fatalf("bad routed frame: %#v", in.Frame)
		}
	case <-time.After(time.Second):
		t.Fatal("DATA did not reach owner")
	}
	select {
	case <-carrier.inputs:
		t.Fatal("striped DATA entered carrier-local Pump")
	default:
	}
}

func checkRetiredStripedDATADropsButUnknownStreamFails(t *testing.T) {
	g := newStripeGroup(wire.ID{6}, 4)
	owner := stripeTestLane(g, "a01", true)
	carrier := stripeTestLane(g, "a02", true)
	defer owner.cancel()
	defer carrier.cancel()
	if err := g.attach(owner); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(carrier); err != nil {
		t.Fatal(err)
	}
	if err := g.register(1, owner); err != nil {
		t.Fatal(err)
	}
	g.retire(1, owner)
	late := wire.Frame{Type: wire.Data, Stream: 1, Payload: []byte("late")}
	if err := carrier.deliver(late); err != nil {
		t.Fatalf("late retired DATA killed carrier: %v", err)
	}
	unknown := wire.Frame{Type: wire.Data, Stream: 3, Payload: []byte("unknown")}
	if err := carrier.deliver(unknown); !errors.Is(err, errStripeStream) {
		t.Fatalf("unknown stream was not rejected: %v", err)
	}
}

func checkStripeHubBoundsLiveGenerationsAndPrunesDetached(t *testing.T) {
	h := newStripeHub()
	lanes := make([]*lane, 0, maxStripeGroupsPerUser)
	for i := 0; i < maxStripeGroupsPerUser; i++ {
		logical := wire.ID{byte(i + 1)}
		g := h.group(logical, uint64(i+1))
		if g == nil {
			t.Fatalf("group %d unexpectedly rejected", i)
		}
		l := stripeTestLane(g, "a01", false)
		lanes = append(lanes, l)
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	if g := h.group(wire.ID{99}, 99); g != nil {
		t.Fatal("live generation bound was bypassed")
	}
	first := lanes[0]
	first.stripe.detach(first)
	first.cancel()
	if g := h.group(wire.ID{100}, 100); g == nil {
		t.Fatal("detached generation was not pruned")
	}
	for _, l := range lanes[1:] {
		l.stripe.detach(l)
		l.cancel()
	}
}

func TestStripeReadySignalFillsAllPhysicalSlots(t *testing.T) {
	g := newStripeGroup(wire.ID{5}, 3)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	l.ready.Store(false)
	l.physicalSlots = make(chan struct{}, 2)
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	leases := make(chan stripeLease, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			lease, err := g.acquire(context.Background())
			if err != nil {
				errs <- err
				return
			}
			leases <- lease
		}()
	}
	time.Sleep(10 * time.Millisecond)
	l.ready.Store(true)
	g.signal()
	got := make([]stripeLease, 0, 2)
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case err := <-errs:
			t.Fatal(err)
		case lease := <-leases:
			got = append(got, lease)
		case <-deadline:
			t.Fatalf("only %d/%d physical slots were filled after one readiness signal", len(got), 2)
		}
	}
	for _, lease := range got {
		lease.release()
	}
}

func TestStripeHubCleanupSerializesAttach(t *testing.T) {
	h := newStripeHub()
	epoch, ok := h.beginCleanup(0)
	if !ok || epoch == 0 {
		t.Fatal("cleanup election failed")
	}
	logical := wire.ID{1}
	l := &lane{logicalSession: logical, generation: 1, account: servercfg.Account{ID: "a01"}}
	if err := h.attach(l); !errors.Is(err, errStripeUnavailable) {
		t.Fatalf("attach entered during cleanup: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- h.waitCleanup(context.Background()) }()
	select {
	case <-done:
		t.Fatal("cleanup waiter released before completion")
	case <-time.After(10 * time.Millisecond):
	}
	h.endCleanup(epoch, true)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup waiter did not release")
	}
	if err := h.attach(l); err != nil {
		t.Fatalf("attach did not resume after cleanup: %v", err)
	}
}

func TestStripeFastWaiterGetsNextReleasedPhysicalSlot(t *testing.T) {
	g := newStripeGroup(wire.ID{3}, 8)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	// Model one bulk APPEND already in flight.
	l.physicalSlots <- struct{}{}

	bulk := make(chan stripeLease, 1)
	fast := make(chan stripeLease, 1)
	errs := make(chan error, 2)
	go func() {
		lease, err := g.acquireForClass(context.Background(), nil, false)
		if err != nil {
			errs <- err
			return
		}
		bulk <- lease
	}()
	time.Sleep(5 * time.Millisecond)
	go func() {
		lease, err := g.acquireForClass(context.Background(), nil, true)
		if err != nil {
			errs <- err
			return
		}
		fast <- lease
	}()
	deadline := time.Now().Add(time.Second)
	for g.fastWaiters.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if g.fastWaiters.Load() == 0 {
		t.Fatal("fast waiter did not enter scheduler")
	}
	// Complete the in-flight bulk APPEND. The fast waiter must win this slot
	// even though the waiting bulk goroutine entered the scheduler first.
	<-l.physicalSlots
	g.signal()
	var fastLease stripeLease
	select {
	case err := <-errs:
		t.Fatal(err)
	case fastLease = <-fast:
	case lease := <-bulk:
		lease.release()
		t.Fatal("bulk waiter overtook latency-sensitive DATA")
	case <-time.After(time.Second):
		t.Fatal("fast waiter did not receive released slot")
	}
	select {
	case lease := <-bulk:
		lease.release()
		t.Fatal("bulk acquired while fast lease was still held")
	default:
	}
	fastLease.release()
	select {
	case err := <-errs:
		t.Fatal(err)
	case lease := <-bulk:
		lease.release()
	case <-time.After(time.Second):
		t.Fatal("bulk did not resume after fast lease released")
	}
}

func checkStripeFirstActiveStreamWakesEveryReader(t *testing.T) {
	g := newStripeGroup(wire.ID{11}, 9)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	a.readWake = make(chan struct{}, 1)
	b.readWake = make(chan struct{}, 1)
	defer a.cancel()
	defer b.cancel()
	if err := g.attach(a); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(b); err != nil {
		t.Fatal(err)
	}
	if _, err := g.allocate(a); err != nil {
		t.Fatal(err)
	}
	for name, ch := range map[string]<-chan struct{}{"a01": a.readWake, "a02": b.readWake} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("reader %s was not woken by logical 0->1 transition", name)
		}
	}
}

func checkStripeLateCarrierWakesWhenStreamAlreadyActive(t *testing.T) {
	g := newStripeGroup(wire.ID{12}, 10)
	a := stripeTestLane(g, "a01", false)
	a.readWake = make(chan struct{}, 1)
	defer a.cancel()
	if err := g.attach(a); err != nil {
		t.Fatal(err)
	}
	if _, err := g.allocate(a); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.readWake:
	default:
	}
	b := stripeTestLane(g, "a02", false)
	b.readWake = make(chan struct{}, 1)
	defer b.cancel()
	if err := g.attach(b); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.readWake:
	case <-time.After(time.Second):
		t.Fatal("late carrier reader remained in idle backoff")
	}
}

func checkStripeSelectionDoesNotConsumePhysicalSlot(t *testing.T) {
	g := newStripeGroup(wire.ID{13}, 11)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	target, err := g.selectFor(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if target != l || len(l.physicalSlots) != 0 {
		t.Fatalf("selection consumed a physical slot: target=%p want=%p held=%d", target, l, len(l.physicalSlots))
	}
	lease, err := g.acquireLaneForClass(context.Background(), target, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.physicalSlots) != 1 {
		t.Fatalf("physical reservation missing after explicit acquire: held=%d", len(l.physicalSlots))
	}
	lease.release()
}

func checkStripePhysicalCapacityTracksLiveWriters(t *testing.T) {
	g := newStripeGroup(wire.ID{14}, 12)
	l := stripeTestLane(g, "a01", false)
	l.physicalSlots = make(chan struct{}, 4)
	capacity := &stripeCapacityMail{parallel: 2}
	l.mail = capacity
	defer l.cancel()
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	one, err := g.acquireLaneForClass(context.Background(), l, false)
	if err != nil {
		t.Fatal(err)
	}
	two, err := g.acquireLaneForClass(context.Background(), l, false)
	if err != nil {
		one.release()
		t.Fatal(err)
	}
	if len(l.physicalSlots) != 2 {
		one.release()
		two.release()
		t.Fatalf("initial live capacity not used: held=%d", len(l.physicalSlots))
	}
	one.release()
	two.release()

	capacity.parallel = 1
	one, err = g.acquireLaneForClass(context.Background(), l, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if extra, err := g.acquireLaneForClass(ctx, l, false); err == nil {
		extra.release()
		one.release()
		t.Fatal("scheduler ignored writer-capacity shrink")
	}
	one.release()

	capacity.parallel = 2
	one, err = g.acquireLaneForClass(context.Background(), l, false)
	if err != nil {
		t.Fatal(err)
	}
	two, err = g.acquireLaneForClass(context.Background(), l, false)
	if err != nil {
		one.release()
		t.Fatal("scheduler did not recover restored writer capacity:", err)
	}
	one.release()
	two.release()
}

func checkStripeStreamSpaceUsesFullMetadataBudget(t *testing.T) {
	g := newStripeGroup(wire.ID{15}, 13)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	g.nextID = maxStripeStreamID
	id, err := g.allocate(l)
	if err != nil {
		t.Fatal(err)
	}
	if id != maxStripeStreamID {
		t.Fatalf("last stream id=%d want=%d", id, maxStripeStreamID)
	}
	if _, err = g.allocate(l); !errors.Is(err, errStripeSessionLimit) {
		t.Fatalf("stream space did not stop at bounded metadata limit: %v", err)
	}
}

func checkStripeRejectsStaleCarrierEpoch(t *testing.T) {
	g := newStripeGroup(wire.ID{16}, 14)
	current := stripeTestLane(g, "a01", false)
	current.carrierEpoch = 2
	defer current.cancel()
	if err := g.attach(current); err != nil {
		t.Fatal(err)
	}
	stale := stripeTestLane(g, "a01", false)
	stale.carrierEpoch = 1
	defer stale.cancel()
	if err := g.attach(stale); !errors.Is(err, errStripeUnavailable) {
		t.Fatalf("stale carrier attach=%v", err)
	}
	if got := g.lanes["a01"]; got != current {
		t.Fatal("stale carrier replaced current authenticated epoch")
	}
}

func checkFastSelectionSkipsFullPhysicalCarrierBeforeTakingGrant(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	full := stripeTestLane(g, "a01", false)
	spare := stripeTestLane(g, "a02", false)
	defer full.cancel()
	defer spare.cancel()
	full.markRemoteProgress()
	spare.markRemoteProgress()
	if err := g.attach(full); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(spare); err != nil {
		t.Fatal(err)
	}
	full.physicalSlots <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	target, key, err := g.takeRemoteGrantForClass(ctx, nil, true)
	if err != nil || target != spare || key.Account != "a02" {
		t.Fatal("fast DATA picked saturated account", target, key, err)
	}
	if len(full.fastGrants) != 1 {
		t.Fatal("full carrier's grant was consumed")
	}
}

func TestFastPhysicalReservationProtectsSelectionAndReleasesOnCancellation(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", true)
	defer l.cancel()
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	// Priority spans grant preparation, then ends as soon as the slot is reserved.
	g.fastWaiters.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	if _, err := g.acquireLaneForClass(ctx, l, false); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatal("bulk overtook fast reservation", err)
	}
	cancel()
	lease, ok := g.tryAcquireFastLane(l)
	if !ok || len(l.physicalSlots) != 1 {
		t.Fatal("fast slot not reserved")
	}
	if _, ok = g.tryAcquireFastLane(l); ok {
		t.Fatal("physical slot overbooked")
	}
	g.fastWaiters.Add(-1)
	g.signal()
	l.cancel()
	_ = l.writeBatchPhysicalPrepared(l, nil, nil, nil, &lease)
	if len(l.physicalSlots) != 0 || g.fastWaiters.Load() != 0 {
		t.Fatal("cancellation leaked slot or priority")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioStripe(t *testing.T) {
	t.Log("check=checkStripeUsesLogicalSessionWideStreamIDs")
	checkStripeUsesLogicalSessionWideStreamIDs(t)
	t.Log("check=checkStripePhysicalSlotsSpreadAcrossCarriers")
	checkStripePhysicalSlotsSpreadAcrossCarriers(t)
	t.Log("check=checkStripePrefersNonOwnerCarrierForDATA")
	checkStripePrefersNonOwnerCarrierForDATA(t)
	t.Log("check=checkStripedDATAIsDeliveredToOwnerPump")
	checkStripedDATAIsDeliveredToOwnerPump(t)
	t.Log("check=checkRetiredStripedDATADropsButUnknownStreamFails")
	checkRetiredStripedDATADropsButUnknownStreamFails(t)
	t.Log("check=checkStripeHubBoundsLiveGenerationsAndPrunesDetached")
	checkStripeHubBoundsLiveGenerationsAndPrunesDetached(t)
	t.Log("check=checkStripeFirstActiveStreamWakesEveryReader")
	checkStripeFirstActiveStreamWakesEveryReader(t)
	t.Log("check=checkStripeLateCarrierWakesWhenStreamAlreadyActive")
	checkStripeLateCarrierWakesWhenStreamAlreadyActive(t)
	t.Log("check=checkStripeSelectionDoesNotConsumePhysicalSlot")
	checkStripeSelectionDoesNotConsumePhysicalSlot(t)
	t.Log("check=checkStripePhysicalCapacityTracksLiveWriters")
	checkStripePhysicalCapacityTracksLiveWriters(t)
	t.Log("check=checkStripeStreamSpaceUsesFullMetadataBudget")
	checkStripeStreamSpaceUsesFullMetadataBudget(t)
	t.Log("check=checkStripeRejectsStaleCarrierEpoch")
	checkStripeRejectsStaleCarrierEpoch(t)
	t.Log("check=checkFastSelectionSkipsFullPhysicalCarrierBeforeTakingGrant")
	checkFastSelectionSkipsFullPhysicalCarrierBeforeTakingGrant(t)
}
