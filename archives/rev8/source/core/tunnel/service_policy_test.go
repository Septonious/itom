package tunnel

import (
	"context"
	"itom.local/core/datapath"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestServicePolicyBoundsSlowLiteralsWithoutChangingRTT(t *testing.T) {
	l := &lane{}
	l.rtt.Store(42)
	for i := 0; i < 8; i++ {
		l.observeDataService(480<<10, 2*time.Second, true)
	}
	if target := l.serviceBatchTarget(); target < 32<<10 || target > 64<<10 {
		t.Fatal("slow service turn unbounded", target)
	}
	if l.rtt.Load() != 42 {
		t.Fatal("physical service contaminated application RTT")
	}
	before := l.serviceSamples
	l.observeDataService(1<<20, time.Millisecond, false)
	if l.serviceSamples != before {
		t.Fatal("unknown APPEND fabricated capacity")
	}
	for i := 0; i < 64; i++ {
		l.observeDataService(480<<10, 80*time.Millisecond, true)
	}
	if target := l.serviceBatchTarget(); target != datapath.BulkBatchTargetBytes {
		t.Fatal("recovered capacity stayed throttled", target)
	}
}

func TestRev2FastControlSocketSamplesCannotCorruptBulkCapacity(t *testing.T) {
	l := &lane{}
	for i := 0; i < 8; i++ {
		l.observeDataServiceClass(480<<10, 80*time.Millisecond, true, false)
	}
	beforeTarget, beforeCost := l.serviceBatchTarget(), l.predictedServiceMS(false)
	for i := 0; i < 100; i++ {
		l.observeDataServiceClass(512, 2*time.Second, true, true)
	}
	if l.serviceBatchTarget() != beforeTarget || l.predictedServiceMS(false) != beforeCost || l.serviceSamples != 8 {
		t.Fatal("small control-folder DATA poisoned the independent bulk service estimate")
	}
	if l.fastServiceSamples != 100 {
		t.Fatal("actual fast service was not observed separately")
	}
}

func TestRev2PlacementSeesAdmissionBeforeThePhysicalSocketClaim(t *testing.T) {
	l := &lane{}
	for i := 0; i < 8; i++ {
		l.observeDataService(480<<10, 80*time.Millisecond, true)
	}
	before := l.predictedServiceMS(false)
	l.physicalActive.Store(3)
	if l.predictedServiceMS(false) <= before {
		t.Fatal("concurrent grant preparations remain invisible to account selection")
	}
	if l.predictedServiceMS(true) != 0 {
		t.Fatal("bulk preparation contaminated independent short service")
	}
}
func TestQuotaCoalescingPreservesOriginalGrantAndDebt(t *testing.T) {
	l := &lane{ctx: context.Background(), maintenanceQueue: make(chan queuedControl, 512)}
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Account: "a01", User: "12345678", Session: wire.ID{2}}
	for i := uint64(1); i <= 100; i++ {
		f, err := wire.EncodeControl(0, wire.CleanupStatus{Key: key, Used: wire.Quota{Bytes: i * 1000, Messages: i}, Registered: wire.Quota{Bytes: i * 1000, Messages: i}})
		if err != nil {
			t.Fatal(err)
		}
		if err = l.enqueueQuotaControl(l.ctx, queuedControl{Frame: f, added: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if len(l.maintenanceQueue) != 1 || len(l.quotaControlLatest) != 1 {
		t.Fatal("obsolete status flooded writer")
	}
	item := l.resolveQuotaControl(<-l.maintenanceQueue)
	decoded, err := wire.DecodeControl(item.Frame)
	if err != nil {
		t.Fatal(err)
	}
	status := decoded.(wire.CleanupStatus)
	if status.Key != key || status.Registered.Messages != 100 || status.Released.Messages != 0 {
		t.Fatal("coalescing changed ownership or refunded debt", status)
	}
	if len(l.quotaControlLatest) != 0 {
		t.Fatal("dequeued status retained")
	}
}

func TestRev2ColdPlacementSeesPendingFsyncBeforeAnyServiceSamples(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	busy, spare := stripeTestLane(g, "a01", true), stripeTestLane(g, "a02", true)
	defer busy.cancel()
	defer spare.cancel()
	if e := g.attach(busy); e != nil {
		t.Fatal(e)
	}
	if e := g.attach(spare); e != nil {
		t.Fatal(e)
	}
	busy.physicalActive.Store(1)
	if busy.predictedServiceMS(false) <= spare.predictedServiceMS(false) {
		t.Fatal("cold grant fsync remained invisible until four APPEND samples")
	}
	g.mu.Lock()
	keys := []string{"a01", "a02"}
	g.prioritizeBulkTargetsLocked(keys)
	g.mu.Unlock()
	if keys[0] != "a02" {
		t.Fatal("cold batch piled behind pending durable admission", keys)
	}
	if busy.serviceSamples != 0 || spare.serviceSamples != 0 {
		t.Fatal("placement manufactured a service observation")
	}
}
