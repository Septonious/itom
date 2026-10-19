package wallet

import (
	"errors"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func TestRecoveryPreservesSpentDebtAndRejectsForeignWalletHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.db")
	w, err := Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	old := w.Session()
	base := wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: old, User: "12345678", Account: "a01"}, Limit: wire.Quota{Bytes: 10000, Messages: 10}}
	if err = w.Accept(base); err != nil {
		t.Fatal(err)
	}
	r, _, err := w.Spend(base.Key, "up", []byte("unknown-APPEND"), func([]byte) (uint64, error) { return 1000, nil })
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	w, err = Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	entry, err := w.ReconcileOffer(base)
	if err != nil || !entry.Fenced || entry.Used != r.Total || len(entry.Pending) != 1 {
		t.Fatal("lost durable Spend", entry, err)
	}
	if _, _, err = w.Spend(base.Key, "up", []byte("retry"), func([]byte) (uint64, error) { return 1000, nil }); !errors.Is(err, ErrState) {
		t.Fatal("old recovery offer became spendable", err)
	}
	lost := base
	lost.Key.Grant = 2
	entry, err = w.ReconcileOffer(lost)
	if err != nil || !entry.Fenced || entry.Used != (wire.Quota{}) {
		t.Fatal("unseen own-session allocation not reconciled", entry, err)
	}
	foreign := base
	foreign.Key.Grant = 3
	foreign.Key.Session = wire.ID{99}
	if _, err = w.ReconcileOffer(foreign); !errors.Is(err, ErrMissing) {
		t.Fatal("another wallet's session treated as unspent", err)
	}
	current := base
	current.Key.Grant = 4
	current.Key.Session = w.Session()
	if _, err = w.ReconcileOffer(current); !errors.Is(err, ErrState) {
		t.Fatal("current-session offer entered recovery", err)
	}
	fresh, err := Open(filepath.Join(t.TempDir(), "fresh.db"), "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err = fresh.ReconcileOffer(lost); !errors.Is(err, ErrMissing) {
		t.Fatal("new wallet claimed lost history", err)
	}
}
