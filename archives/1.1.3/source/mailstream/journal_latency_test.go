package mailstream

import (
	"context"
	"testing"
	"time"
)

// Repeated speedtests retire large generations, not just tiny control prefixes.
// Reusing already durable capacity must not fsync on every collection/recharge.
func TestCollectedBulkReusesDurableHighWaterWithoutDiskWrites(t *testing.T) {
	dir := t.TempDir()
	store, err := openRetention(dir, "bulk-journal", true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	key := []byte("account/bulk")
	a := &mailAccountState{link: &IMAPLink{store: store}, storageKey: key}
	a.mu.Lock()
	err = a.chargeLocked(16 << 20)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before := store.db.Stats()
	for range 20 {
		a.mu.Lock()
		err = a.refundLocked(8 << 20)
		if err == nil {
			err = a.chargeLocked(8 << 20)
		}
		a.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	a.mu.Lock()
	err = a.refundLocked(8 << 20)
	spent, reserved := a.spent, a.reserved
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	after := store.db.Stats()
	if after.TxStats.GetWrite() != before.TxStats.GetWrite() {
		t.Errorf("audited bulk refund/reuse rewrote journal: writes %d -> %d", before.TxStats.GetWrite(), after.TxStats.GetWrite())
	}
	if err = store.close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRetention(dir, "bulk-journal", true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	debt, err := reopened.load(key)
	if err != nil || debt < spent || debt != reserved || debt > maximumRetentionBudget {
		t.Fatal("crash reservation failed to cover attempted bytes", debt, spent, reserved, err)
	}
	t.Logf("20 bulk refund/reuse cycles: %d disk writes; crash recovers conservative %d bytes for %d outstanding", after.TxStats.GetWrite()-before.TxStats.GetWrite(), debt, spent)
}

// Emulate a slow fsync deterministically by holding the store's IO mutex. The
// sole APPEND owner must reserve before publication, while receipt, route and
// verified-refund work remain responsive and admission retains the pending body.
func TestSlowJournalGrowthDoesNotBlockReceiptsOrLoseConcurrentRefund(t *testing.T) {
	store, err := openRetention(t.TempDir(), "slow-journal", true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	id := SessionID{1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{ctx: ctx, store: store}
	rec := &segmentRecord{awaiting: map[uint64]bool{7: true}}
	a := &mailAccountState{link: l, id: id, storageKey: []byte("account/slow"), streams: map[uint32]bool{}, segments: map[segmentKey]*segmentRecord{{session: id}: rec}}
	l.accounts = []*mailAccountState{a}
	a.mu.Lock()
	err = a.chargeLocked(reservationQuantum)
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	started, charged, responsive := make(chan struct{}), make(chan error, 1), make(chan error, 1)
	go func() {
		a.mu.Lock()
		close(started)
		err := a.chargeLocked(reservationQuantum)
		a.mu.Unlock()
		charged <- err
	}()
	<-started
	go func() {
		a.sentPacket(header(sealed, id, 7))
		l.TrackStream(id, 7, 11)
		if err := l.Acknowledge(id, 7); err != nil {
			responsive <- err
			return
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.queuedBytes != reservationQuantum {
			responsive <- ErrCapacity
			return
		}
		responsive <- a.refundLocked(reservationQuantum / 2)
	}()
	received := false
	select {
	case err = <-responsive:
		received = true
		if err != nil {
			t.Error("receipt/refund while journal blocked", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Error("slow durable reservation blocked account receipts and verified refund")
	}
	select {
	case <-charged:
		t.Error("publication reservation completed before durable IO")
	default:
	}
	store.mu.Unlock()
	if err = <-charged; err != nil {
		t.Fatal(err)
	}
	// The responsive worker may finish only after unblocking the old code.
	if !received {
		<-responsive
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.spent != 3*reservationQuantum/2 || a.queuedBytes != 0 || len(rec.awaiting) != 0 || !a.streams[11] || a.reserved < a.spent {
		t.Error("concurrent receipt/refund or admission accounting lost", a.spent, a.queuedBytes, a.reserved, rec.awaiting, a.streams)
	}
}

func TestCleanLinkClosePersistsExactDebtAfterHighWaterReuse(t *testing.T) {
	dir := t.TempDir()
	store, err := openRetention(dir, "clean-high-water", true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{ctx: ctx, cancel: cancel, store: store, incoming: make(chan []byte)}
	key := []byte("account/clean")
	a := &mailAccountState{link: l, storageKey: key}
	l.accounts = []*mailAccountState{a}
	a.mu.Lock()
	err = a.chargeLocked(16 << 20)
	if err == nil {
		err = a.refundLocked(8 << 20)
	}
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRetention(dir, "clean-high-water", true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	debt, err := reopened.load(key)
	if err != nil || debt != 8<<20 {
		t.Fatal("clean stop retained unused high water", debt, err)
	}
}
