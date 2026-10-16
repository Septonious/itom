package tunnel

import (
	"context"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func checkFlowCreditCoalescesBeforeBackpressuredWriter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &lane{ctx: ctx, controlQueue: make(chan queuedControl, 2)}
	first := time.Now()
	for n := uint64(1); n <= 10000; n++ {
		frame, err := wire.EncodeControl(1, wire.Credit{MaxStream: n, MaxData: n + 10})
		if err != nil {
			t.Fatal(err)
		}
		if err = l.sendCarrierControl(ctx, frame); err != nil {
			t.Fatal("cumulative updates filled queue", err)
		}
	}
	if len(l.controlQueue) != 1 || len(l.flowControlLatest) != 1 {
		t.Fatal("queue grew with obsolete credit", len(l.controlQueue), len(l.flowControlLatest))
	}
	old, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 3, MaxData: 13})
	if err := l.sendCarrierControl(ctx, old); err != nil {
		t.Fatal(err)
	}
	latest := l.resolveFlowControl(<-l.controlQueue)
	decoded, err := wire.DecodeControl(latest.Frame)
	if err != nil {
		t.Fatal(err)
	}
	credit := decoded.(wire.Credit)
	if credit.MaxStream != 10000 || credit.MaxData != 10010 {
		t.Fatal("coalescing lost newest or rolled back credit", credit)
	}
	if latest.added.Before(first) || len(l.flowControlLatest) != 0 || latest.coalesced {
		t.Fatal("resolved credit retained queue state")
	}
	// Once the writer takes a frame, the next update gets a distinct queued token.
	next, _ := wire.EncodeControl(1, wire.Credit{MaxStream: 10001, MaxData: 10011})
	if err = l.sendCarrierControl(ctx, next); err != nil {
		t.Fatal(err)
	}
	if len(l.controlQueue) != 1 {
		t.Fatal("resolved credit suppressed a new update")
	}
}

func checkCleanupStatusCoalescingKeepsScopeAndMonotonicProof(t *testing.T) {
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: wire.ID{2}, User: "12345678", Account: "a01"}
	old := wire.CleanupStatus{Key: key, Used: wire.Quota{Bytes: 2000, Messages: 2}, Registered: wire.Quota{Bytes: 2000, Messages: 2}, Released: wire.Quota{Bytes: 1000, Messages: 1}}
	newer := old
	newer.Released = newer.Registered
	newer.Closed = true
	queued := func(s wire.CleanupStatus) queuedControl {
		f, err := wire.EncodeControl(0, s)
		if err != nil {
			t.Fatal(err)
		}
		return queuedControl{Frame: f, added: time.Now()}
	}
	batch := []queuedControl{queued(old)}
	batch = coalesceCleanupStatus(batch, queued(newer))
	batch = coalesceCleanupStatus(batch, queued(old))
	if len(batch) != 1 {
		t.Fatal("same monotonic proof duplicated", len(batch))
	}
	got, err := wire.DecodeControl(batch[0].Frame)
	if err != nil || got.(wire.CleanupStatus) != newer {
		t.Fatal("proof rolled back", got, err)
	}
	other := newer
	other.Key.Account = "a02"
	batch = coalesceCleanupStatus(batch, queued(other))
	if len(batch) != 2 {
		t.Fatal("different account proof was coalesced")
	}
}

func checkFastSelectionAvoidsDescriptorControlDebt(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	defer a.cancel()
	defer b.cancel()
	g.attach(a)
	g.attach(b)
	a.controlDebt.Store(96)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	selected, err := g.selectForClass(ctx, nil, true)
	if err != nil || selected != b {
		t.Fatal("fast DATA selected blocked descriptor path", selected, err)
	}
	if len(a.physicalSlots) != 0 || len(b.physicalSlots) != 0 {
		t.Fatal("selection held a physical slot")
	}
}

func checkFastGrantSelectionUsesLeastQueuedUsableCarrier(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	owner := stripeTestLane(g, "a01", false)
	busy := stripeTestLane(g, "a02", false)
	spare := stripeTestLane(g, "a03", false)
	for _, l := range []*lane{owner, busy, spare} {
		defer l.cancel()
		l.markRemoteProgress()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	busy.controlDebt.Store(70)
	spare.physicalSlots <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	selected, key, err := g.takeRemoteGrantForClass(ctx, owner, true)
	if err != nil || selected != owner || key.Account != owner.account.ID {
		t.Fatal("absolute owner avoidance selected queued I/O", selected, key, err)
	}
	// An unusable lowest-score carrier must not block a usable peer.
	spare.controlDebt.Store(0)
	<-spare.physicalSlots
	selected, key, err = g.takeRemoteGrantForClass(ctx, nil, true)
	if err != nil || selected != spare || key.Account != spare.account.ID {
		t.Fatal("least queued usable peer was not selected", selected, key, err)
	}
}

func checkCleanupPreservesHealthyFastCarriersAndDrainsBlockedOne(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := stripeTestLane(g, "a01", false)
	b := stripeTestLane(g, "a02", false)
	c := stripeTestLane(g, "a03", false)
	d := stripeTestLane(g, "a04", false)
	for _, l := range []*lane{a, b, c, d} {
		defer l.cancel()
		g.attach(l)
	}
	c.controlDebt.Store(100)
	d.controlDebt.Store(100)
	if !g.beginCleanup(a) {
		t.Fatal("cleaner did not keep the five-account service ratio")
	}
	if g.beginCleanup(b) {
		t.Fatal("cleaner consumed the final healthy fast carrier")
	}
	if !g.beginCleanup(c) || !g.beginCleanup(d) {
		t.Fatal("blocked control debt could not receive cleanup service")
	}
	g.endCleanup(c)
	c.controlDebt.Store(0)
	if !g.beginCleanup(b) {
		t.Fatal("FIFO waiter did not resume after another fast carrier recovered")
	}
	g.endCleanup(b)
	g.endCleanup(a)
	g.endCleanup(d)
}

func checkFastGrantSelectionAvoidsInFlightDescriptorWriter(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b := stripeTestLane(g, "a01", false), stripeTestLane(g, "a02", false)
	for _, l := range []*lane{a, b} {
		defer l.cancel()
		l.markRemoteProgress()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	a.controlWriting.Store(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	selected, key, err := g.takeRemoteGrantForClass(ctx, b, true)
	if err != nil || selected != b || key.Account != "a02" {
		t.Fatal("empty queue concealed in-flight descriptor APPEND", selected, key, err)
	}
}

func checkFastTargetPrefersPeerReaderWithoutPendingBulk(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a, b := stripeTestLane(g, "a01", true), stripeTestLane(g, "a02", true)
	for _, l := range []*lane{a, b} {
		defer l.cancel()
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	a.unreceivedLocalRecords.Store(32)
	g.mu.Lock()
	keys := []string{"a01", "a02"}
	g.prioritizeFastTargetsLocked(keys, b)
	g.mu.Unlock()
	if keys[0] != "a02" {
		t.Fatal("idle APPEND concealed pending peer FETCH", keys)
	}
}

func TestPeerReaderBacklogTracksPartialReceiptsAndEarlyArrival(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", true)
	l.stripe = g
	defer l.cancel()
	l.obsolete = map[journal.AttemptRef]bool{}
	l.mailReceipts = map[uint64]bool{}
	l.account.Mail.Host = "mail.example"
	l.user = servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	store, err := ledger(t.TempDir(), "data", l.account, l.user, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	l.data = store
	local := journal.GrantRef{Store: store.StoreID()}
	l.registerOutgoing(outgoing{ref: journal.AttemptRef{Grant: local, ID: 1}, counters: []uint64{1, 2}, records: 2, usefulBytes: 65536})
	l.registerOutgoing(outgoing{ref: journal.AttemptRef{Grant: local, ID: 2}, counters: []uint64{3, 4}, records: 2, usefulBytes: 65536})
	l.registerOutgoing(outgoing{ref: journal.AttemptRef{Grant: journal.GrantRef{Store: wire.ID{99}}, ID: 1}, counters: []uint64{50}, records: 1})
	if l.unreceivedLocalRecords.Load() != 4 {
		t.Fatal("control records counted as pending DATA")
	}
	receive := func(counters ...uint64) {
		f, e := wire.EncodeControl(0, wire.MailReceived{Counters: counters})
		if e != nil {
			t.Fatal(e)
		}
		if e = l.process(f); e != nil {
			t.Fatal(e)
		}
	}
	receive(1)
	if g.downloadDelivered.Load() != 0 {
		t.Fatal("partial BODY receipt counted as complete delivery")
	}
	receive(1, 2)
	if g.downloadDelivered.Load() != 65536 {
		t.Fatal("complete BODY receipt omitted useful bytes")
	}
	receive(1, 2)
	if g.downloadDelivered.Load() != 65536 {
		t.Fatal("duplicate receipt inflated rate feedback")
	}
	if l.unreceivedLocalRecords.Load() != 2 {
		t.Fatal("partial receipt cleared unread body")
	}
	if l.unreceivedLocalBytes.Load() != 65536 {
		t.Fatal("partial receipt lost unread batch bytes", l.unreceivedLocalBytes.Load())
	}
	receive(3, 4, 5)
	l.registerOutgoing(outgoing{ref: journal.AttemptRef{Grant: local, ID: 3}, counters: []uint64{5}, records: 1, usefulBytes: 32768})
	if l.unreceivedLocalRecords.Load() != 0 {
		t.Fatal("receipt ahead of tagged APPEND left false backlog")
	}
	if l.unreceivedLocalBytes.Load() != 0 {
		t.Fatal("early receipt left false byte backlog", l.unreceivedLocalBytes.Load())
	}
	if g.downloadDelivered.Load() != 163840 {
		t.Fatal("early completion or partial receipt lost useful bytes", g.downloadDelivered.Load())
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioFlowControl(t *testing.T) {
	t.Log("check=checkFlowCreditCoalescesBeforeBackpressuredWriter")
	checkFlowCreditCoalescesBeforeBackpressuredWriter(t)
	t.Log("check=checkCleanupStatusCoalescingKeepsScopeAndMonotonicProof")
	checkCleanupStatusCoalescingKeepsScopeAndMonotonicProof(t)
	t.Log("check=checkFastSelectionAvoidsDescriptorControlDebt")
	checkFastSelectionAvoidsDescriptorControlDebt(t)
	t.Log("check=checkFastGrantSelectionUsesLeastQueuedUsableCarrier")
	checkFastGrantSelectionUsesLeastQueuedUsableCarrier(t)
	t.Log("check=checkCleanupPreservesHealthyFastCarriersAndDrainsBlockedOne")
	checkCleanupPreservesHealthyFastCarriersAndDrainsBlockedOne(t)
	t.Log("check=checkFastGrantSelectionAvoidsInFlightDescriptorWriter")
	checkFastGrantSelectionAvoidsInFlightDescriptorWriter(t)
	t.Log("check=checkFastTargetPrefersPeerReaderWithoutPendingBulk")
	checkFastTargetPrefersPeerReaderWithoutPendingBulk(t)
}
