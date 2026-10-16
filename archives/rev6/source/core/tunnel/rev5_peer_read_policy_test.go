package tunnel

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// A fast tagged APPEND does not describe the peer's paced download socket.
// Exercise authenticated physical receipts, including partial/duplicate ACKs;
// short records and unknown attempts must not train the bulk policy.
func TestRev5PhysicalReceiptsBoundPacedBodiesAndRecover(t *testing.T) {
	l := &lane{ctx: context.Background(), side: true, stripe: newStripeGroup(wire.ID{1}, 1),
		obsolete: map[journal.AttemptRef]bool{}}
	for i := 0; i < 4; i++ {
		l.observeDataService(350<<10, 40*time.Millisecond, true)
	}
	if l.serviceBatchTarget() != 32<<10 {
		t.Fatal("fast APPEND fabricated proof of peer download capacity")
	}
	var counter uint64
	receipt := func(size int64, age time.Duration, eligible bool, partial bool) {
		t.Helper()
		counter += 2
		l.registerOutgoing(outgoing{ref: journal.AttemptRef{ID: counter}, counters: []uint64{counter - 1, counter}, records: 2,
			usefulBytes: size, receiptStarted: time.Now().Add(-age), receiptEligible: eligible})
		ack := func(ids []uint64) {
			f, err := wire.EncodeControl(0, wire.MailReceived{Counters: ids})
			if err != nil {
				t.Fatal(err)
			}
			if err = l.process(f); err != nil {
				t.Fatal(err)
			}
		}
		if partial {
			ack([]uint64{counter - 1})
			if l.serviceBatchTarget() != datapath.BulkBatchTargetBytes {
				t.Fatal("partial physical BODY trained policy")
			}
		}
		ack([]uint64{counter - 1, counter})
		ack([]uint64{counter - 1, counter}) // a duplicate is not another sample
	}
	for i := 0; i < 16; i++ {
		receipt(32<<10, 100*time.Millisecond, true, false)
	}
	if l.serviceBatchTarget() != datapath.BulkBatchTargetBytes {
		t.Fatal("fast physical receipts did not establish healthy downstream")
	}
	receipt(256<<10, 12*time.Second, true, true)
	if l.serviceBatchTarget() != datapath.BulkBatchTargetBytes {
		t.Fatal("one outlier shrank batch policy")
	}
	receipt(256<<10, 12*time.Second, false, false)
	receipt(512, 12*time.Second, true, false)
	if l.serviceBatchTarget() != datapath.BulkBatchTargetBytes {
		t.Fatal("unqualified or short receipt trained bulk")
	}
	receipt(256<<10, 12*time.Second, true, false)
	if got := l.serviceBatchTarget(); got != 32<<10 {
		t.Fatalf("fast APPEND hides 12s paced peer BODY: target=%d", got)
	}
	// A paced 32 KiB BODY takes over the one-second service budget. It must
	// not reset the cap merely because its smaller size completes sooner.
	for i := 0; i < 8; i++ {
		receipt(32<<10, 1500*time.Millisecond, true, false)
	}
	if l.serviceBatchTarget() != 32<<10 {
		t.Fatal("paced small bodies caused a batch-size oscillation")
	}
	for i := 0; i < 16; i++ {
		receipt(32<<10, 100*time.Millisecond, true, false)
	}
	if got := l.serviceBatchTarget(); got != datapath.BulkBatchTargetBytes {
		t.Fatalf("recovered downstream stayed capped: %d", got)
	}
}

func TestRev5PacedAccountDoesNotShrinkHealthyPoolOrRTT(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	for i := 0; i < 5; i++ {
		l := &lane{ctx: context.Background(), stripe: g, account: servercfg.Account{ID: fmt.Sprintf("a%d", i)}}
		l.ready.Store(true)
		l.rtt.Store(42)
		g.lanes[l.account.ID] = l
		if i == 4 {
			for n := 0; n < 2; n++ {
				l.observePeerReadService(256<<10, time.Now().Add(-12*time.Second), time.Now())
			}
			if l.serviceBatchTarget() != 32<<10 || l.rtt.Load() != 42 {
				t.Fatal("peer BODY pacing changed logical RTT")
			}
		}
	}
	for _, l := range g.lanes {
		if target, _ := l.BulkBatchPolicy(); target != datapath.BulkBatchTargetBytes {
			t.Fatal("one paced account shrank the healthy pool", target)
		}
	}
}

func TestRev5ProviderStateRetryDoesNotWeakenUIDProof(t *testing.T) {
	if retryableReceive(mailbox.ErrChanged) {
		t.Fatal("actual locator UIDVALIDITY mismatch became retry proof")
	}
	if !retryableReceive(mailbox.ErrMailboxStateChanged) {
		t.Fatal("transient provider setup state cannot reconnect")
	}
}

// Authenticated unloaded propagation is separate from BODY service. Four DATA
// shards plus mobile command latency can make a fast small physical receipt
// take two seconds; leaving every batch cold consumes retained message slots.
func TestRev5PeerReadSeparatesUnloadedPropagationFromBodyPacing(t *testing.T) {
	l := &lane{side: true}
	l.idleRTT.Store(800)
	l.rtt.Store(10000) // current queue-inflated RTT must not raise the service budget
	for i := 0; i < 8; i++ {
		l.observePeerReadService(32<<10, time.Now().Add(-2*time.Second), time.Now())
	}
	if l.serviceBatchTarget() != datapath.BulkBatchTargetBytes {
		t.Fatal("unloaded mobile propagation permanently kept healthy BODY service cold")
	}
	for i := 0; i < 2; i++ {
		l.observePeerReadService(256<<10, time.Now().Add(-16*time.Second), time.Now())
	}
	if got := l.serviceBatchTarget(); got > 64<<10 {
		t.Fatal("loaded RTT hid paced BODY service", got)
	}
	if l.idleRTT.Load() != 800 || l.rtt.Load() != 10000 {
		t.Fatal("batch policy modified RTT measurements")
	}
}

func TestRev5KnownPeerBodiesBoundColdAdmissionWithoutBlockingUnknown(t *testing.T) {
	a := servercfg.Account{ID: "a01"}
	a.Mail.Host = "mail.example"
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	l := &lane{side: true, data: s}
	// One known successful bulk copy can gate placement. An unknown physical
	// result remains charged elsewhere but cannot manufacture permanent blockage.
	l.out = []outgoing{{ref: journal.AttemptRef{Grant: journal.GrantRef{Store: s.StoreID()}}, counters: []uint64{1}, receiptStarted: time.Now(), receiptEligible: false}}
	l.refreshUnreceivedLocalLocked()
	if l.peerReadWindowFull() {
		t.Fatal("unknown outcome blocked new repair placement")
	}
	l.out[0].receiptEligible = true
	l.refreshUnreceivedLocalLocked()
	if !l.peerReadWindowFull() {
		t.Fatal("cold BODY backlog admitted another envelope")
	}
	l.peerReadNewestPendingAt.Store(time.Now().Add(-10 * time.Second).UnixNano())
	if l.peerReadWindowFull() || l.peerReadPendingBodies.Load() != 1 || len(l.out[0].counters) != 1 {
		t.Fatal("stale advisory wait blocked recovery or forged a receipt")
	}
	l.refreshUnreceivedLocalLocked()
	l.out[0].counters = nil
	l.refreshUnreceivedLocalLocked()
	if l.peerReadWindowFull() {
		t.Fatal("completed physical receipt failed to reopen placement")
	}
	l.physicalActive.Store(1)
	if !l.peerReadWindowFull() {
		t.Fatal("in-progress admission raced another collector")
	}
	l.physicalActive.Store(0)
	l.observePeerReadService(32<<10, time.Now().Add(-100*time.Millisecond), time.Now())
	l.peerReadPendingBodies.Store(1)
	l.peerReadNewestPendingAt.Store(time.Now().UnixNano())
	if l.peerReadWindowFull() {
		t.Fatal("measured convergence serialized every handshake")
	}
	l.peerReadPendingBodies.Store(2)
	if !l.peerReadWindowFull() {
		t.Fatal("convergence admitted unbounded BODY backlog")
	}
	l.peerReadPendingBodies.Store(0)
	for i := 0; i < 8; i++ {
		l.observePeerReadService(32<<10, time.Now().Add(-100*time.Millisecond), time.Now())
	}
	l.peerReadPendingBodies.Store(4)
	if l.peerReadWindowFull() {
		t.Fatal("trained healthy path lost existing writer parallelism")
	}
}

// No heartbeat, receipt or capacity signal arrives after this selection starts.
// Its own bounded lease must wake it, without changing the old physical record.
func TestRev5KnownPeerLeaseExpiryWakesWithoutExternalTraffic(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", true)
	defer l.cancel()
	l.account.Mail = mailbox.Config{Host: "mail.example"}
	l.user = servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	l.cipher = cleanupFixtureEpoch(t)
	var err error
	l.data, err = ledger(t.TempDir(), "data", l.account, l.user, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.data.Close()
	if err = l.data.EnableAfterInventory(l.account.ID, l.user.ID, l.data.Run()); err != nil {
		t.Fatal(err)
	}
	if err = g.attach(l); err != nil {
		t.Fatal(err)
	}
	l.out = []outgoing{{ref: journal.AttemptRef{Grant: journal.GrantRef{Store: l.data.StoreID()}}, counters: []uint64{1}, receiptStarted: time.Now().Add(-2800 * time.Millisecond), receiptEligible: true}}
	l.refreshUnreceivedLocalLocked()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	chosen, grant, err := g.takeLocalGrantForClass(ctx, nil, false, 32<<10)
	if err != nil || chosen != l {
		t.Fatal("advisory expiry did not wake selector", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("fresh scheduling lease was ignored")
	}
	if l.peerReadPendingBodies.Load() != 1 || len(l.out[0].counters) != 1 {
		t.Fatal("expiry forged receipt or discarded old debt metadata")
	}
	if err = l.releaseReservedLocalGrant(grant); err != nil {
		t.Fatal(err)
	}
	if err = l.data.Audit(); err != nil {
		t.Fatal(err)
	}
}
