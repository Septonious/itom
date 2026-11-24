package wallet_test

import (
	"context"
	"errors"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func waitForAdmissionStop(t *testing.T, w *wallet.Wallet) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		ticket, err := w.Reserve(512)
		if errors.Is(err, wallet.ErrExhausted) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		ticket.Cancel()
		select {
		case <-deadline.C:
			t.Fatal("sender admission did not stop")
		case <-time.After(time.Millisecond):
		}
	}
}
func TestSenderFenceWaitsForAlreadyReservedSpendWithoutInvalidatingItsTicket(t *testing.T) {
	w := walletAt(t, filepath.Join(t.TempDir(), "wallet.db"))
	defer w.Close()
	o := offer(w, 1, 1<<20, 8)
	if err := w.Accept(o); err != nil {
		t.Fatal(err)
	}
	ticket, err := w.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan wire.StorageRelease, 1)
	failed := make(chan error, 1)
	go func() {
		release, err := w.FenceAfterAdmissions(ctx, o.Key)
		if err != nil {
			failed <- err
			return
		}
		done <- release
	}()
	waitForAdmissionStop(t, w)
	report, _, err := ticket.Commit("fast", []byte("already reserved ciphertext"), make([]byte, 32), encoded)
	if err != nil {
		t.Fatal("fence invalidated an admitted writer", err)
	}
	select {
	case release := <-done:
		if release.Final != report.Total || release.Final.Messages != 1 {
			t.Fatal("final fence omitted admitted durable spend", release, report)
		}
	case err := <-failed:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("fence did not follow ticket completion")
	}
	if _, err := w.Reserve(512); !errors.Is(err, wallet.ErrExhausted) {
		t.Fatal("fenced grant reopened", err)
	}
	entries, err := w.Entries()
	if err != nil || len(entries) != 1 || !entries[0].Fenced || entries[0].Used != report.Total || len(entries[0].Pending) != 1 {
		t.Fatal("final fence lost original debt", entries, err)
	}
}
func TestCanceledSenderFenceRestoresOnlyExistingAdmissionAndPreservesSpend(t *testing.T) {
	w := walletAt(t, filepath.Join(t.TempDir(), "wallet.db"))
	defer w.Close()
	o := offer(w, 1, 1<<20, 8)
	if err := w.Accept(o); err != nil {
		t.Fatal(err)
	}
	ticket, err := w.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.Cancel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := w.FenceAfterAdmissions(ctx, o.Key); done <- err }()
	waitForAdmissionStop(t, w)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	extra, err := w.Reserve(512)
	if err != nil {
		t.Fatal("canceled volatile stop stranded existing grant", err)
	}
	extra.Cancel()
	report, _, err := ticket.Commit("fast", []byte("survives canceled fence"), make([]byte, 32), encoded)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := w.Entries()
	if err != nil || entries[0].Fenced || entries[0].Used != report.Total || len(entries[0].Pending) != 1 {
		t.Fatal("cancel changed durable authority", entries, err)
	}
}
