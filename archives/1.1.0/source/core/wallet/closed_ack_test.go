package wallet_test

import (
	"context"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func TestFinalSpendAcknowledgementSurvivesRestartWithoutClaimingPhysicalCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original-wallet.db")
	w := walletAt(t, path)
	o := offer(w, 1, 1<<20, 8)
	if err := w.Accept(o); err != nil {
		t.Fatal(err)
	}
	ticket, err := w.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	report, _, err := ticket.Commit("fast", []byte("original physical intent remains charged"), make([]byte, 32), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.FenceAfterAdmissions(context.Background(), o.Key); err != nil {
		t.Fatal(err)
	}
	// Registration alone is not a final-spend or cleanup acknowledgement.
	status := wire.CleanupStatus{Key: o.Key, Used: report.Total, Registered: report.Total}
	if err = w.Acknowledge(status); err != nil {
		t.Fatal(err)
	}
	entries, _ := w.Entries()
	if entries[0].IssuerClosed {
		t.Fatal("registration invented final-spend acknowledgement")
	}
	status.Closed = true
	// Registered is unchanged: this previously returned before persisting Closed.
	if err = w.Acknowledge(status); err != nil {
		t.Fatal(err)
	}
	w.Close()
	w = walletAt(t, path)
	defer w.Close()
	entries, err = w.Entries()
	if err != nil || len(entries) != 1 || !entries[0].IssuerClosed || entries[0].Retired || entries[0].Used != report.Total || !entries[0].Fenced {
		t.Fatal("final acknowledgement lost or physical cleanup fabricated", entries, err)
	}
	status.Closed = false
	if err = w.Acknowledge(status); err != nil {
		t.Fatal(err)
	}
	entries, _ = w.Entries()
	if !entries[0].IssuerClosed {
		t.Fatal("older status rolled back durable final acknowledgement")
	}
	if _, err = w.Reserve(256); err == nil {
		t.Fatal("closed original grant became spendable")
	}
}
