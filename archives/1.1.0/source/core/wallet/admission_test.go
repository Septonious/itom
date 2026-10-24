package wallet_test

import (
	"bytes"
	"errors"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func walletAt(t *testing.T, path string) *wallet.Wallet {
	t.Helper()
	w, e := wallet.Open(path, "12345678", wire.ID{1})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func offer(w *wallet.Wallet, grant, capacity, messages uint64) wire.StorageGrant {
	return wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: grant, Session: w.Session(), User: "12345678", Account: "account1"}, Limit: wire.Quota{Bytes: capacity, Messages: messages}}
}

// Size is deliberately independent of MIME formatting. The runtime must supply
// the canonical MIME encoder; this checks atomic authority, not performance.
func encoded(b []byte) (uint64, error) { return uint64(len(b)), nil }
func TestConcurrentAdmissionNeverPromisesTheSameLastGrantSpace(t *testing.T) {
	w := walletAt(t, filepath.Join(t.TempDir(), "wallet.db"))
	defer w.Close()
	if e := w.Accept(offer(w, 1, 10000, 5)); e != nil {
		t.Fatal(e)
	}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	tickets := make(chan *wallet.Ticket, 32)
	for range 32 {
		wg.Go(func() {
			ticket, e := w.Reserve(2000)
			if e == nil {
				admitted.Add(1)
				tickets <- ticket
			} else if !errors.Is(e, wallet.ErrExhausted) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	close(tickets)
	if n := admitted.Load(); n != 5 {
		t.Fatal("admitted count", n)
	}
	for ticket := range tickets {
		ticket.Cancel()
	}
	if _, e := w.Reserve(10000); e != nil {
		t.Fatal("unused cancellation leaked credit", e)
	}
}
func TestOneTicketCannotPublishTwoBodiesEvenWithConcurrentCommit(t *testing.T) {
	w := walletAt(t, filepath.Join(t.TempDir(), "wallet.db"))
	defer w.Close()
	if e := w.Accept(offer(w, 1, 20000, 10)); e != nil {
		t.Fatal(e)
	}
	ticket, e := w.Reserve(4000)
	if e != nil {
		t.Fatal(e)
	}
	var committed atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _, e := ticket.Commit("fast", []byte("sealed record"), bytes.Repeat([]byte{2}, 32), encoded)
			if e == nil {
				committed.Add(1)
			} else if !errors.Is(e, wallet.ErrState) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if committed.Load() != 1 {
		t.Fatal("ticket was not consumed atomically", committed.Load())
	}
	entries, e := w.Entries()
	if e != nil || len(entries) != 1 || entries[0].Used.Messages != 1 {
		t.Fatal(entries, e)
	}
}
func TestMissingHistoryCannotProduceZeroSpendProofAfterReinstall(t *testing.T) {
	dir := t.TempDir()
	w := walletAt(t, filepath.Join(dir, "old.db"))
	grant := offer(w, 1, 20000, 10)
	if e := w.Accept(grant); e != nil {
		t.Fatal(e)
	}
	ticket, e := w.Reserve(4000)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = ticket.Commit("bulk", []byte("sealed record"), bytes.Repeat([]byte{3}, 32), encoded); e != nil {
		t.Fatal(e)
	}
	w.Close()
	// Fresh app data has no authenticated proof of the old writer's consumption.
	fresh := walletAt(t, filepath.Join(dir, "fresh.db"))
	defer fresh.Close()
	if _, e = fresh.ReconcileOffer(grant); !errors.Is(e, wallet.ErrMissing) {
		t.Fatal("invented old zero-spend history", e)
	}
	reopened := walletAt(t, filepath.Join(dir, "old.db"))
	defer reopened.Close()
	entries, e := reopened.Entries()
	if e != nil || len(entries) != 1 || entries[0].Used.Messages != 1 || !entries[0].Fenced || len(entries[0].Pending) != 1 {
		t.Fatal("durable ambiguous body lost after restart", entries, e)
	}
	if _, e = reopened.Reserve(1000); !errors.Is(e, wallet.ErrExhausted) {
		t.Fatal("old session became writable", e)
	}
}
func TestAdmissionSelectsConcreteRemainingAllowanceWithoutFencing(t *testing.T) {
	w := walletAt(t, filepath.Join(t.TempDir(), "wallet.db"))
	defer w.Close()
	if e := w.Accept(offer(w, 1, 1000, 10)); e != nil {
		t.Fatal(e)
	}
	if e := w.Accept(offer(w, 2, 20000, 10)); e != nil {
		t.Fatal(e)
	}
	ticket, e := w.Reserve(3000)
	if e != nil || ticket.Key.Grant != 2 {
		t.Fatal(ticket, e)
	}
	entries, e := w.Entries()
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range entries {
		if entry.Fenced {
			t.Fatal("normal placement fenced a grant")
		}
	}
}
