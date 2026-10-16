package wallet

import (
	"bytes"
	"path/filepath"
	"testing"

	"itom.local/core/wire"
)

func TestEnvelopeSpendPreservesUncertainDebtAndRestartDescriptor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wallet.db")
	w, err := Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a128"}
	if err = w.Accept(wire.StorageGrant{Key: k, Limit: wire.Quota{Bytes: 10000, Messages: 3}}); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{9}, 32)
	charge := func(b []byte) (uint64, error) { return uint64(len(b) + 100), nil }
	r1, b1, err := w.SpendEnvelope(k, "itom-up-data-00", []byte("encrypted-one"), key, charge)
	if err != nil {
		t.Fatal(err)
	}
	r2, _, err := w.SpendEnvelope(k, "itom-up-data-00", []byte("encrypted-two"), key, charge)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Total.Bytes != r1.Total.Bytes+r2.Attempt.EncodedBytes || r2.Total.Messages != 2 {
		t.Fatal("physical spends were not cumulative")
	}
	if _, _, err = wire.OpenDataEnvelope(b1, key, r1.Attempt.EncodedBytes); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	w, err = Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rows, err := w.Entries()
	if err != nil || len(rows) != 1 || rows[0].Used != r2.Total || len(rows[0].Pending) != 2 || *rows[0].Pending[0].Attempt != *r1.Attempt {
		t.Fatal("restart lost physical uncertainty/descriptor", err)
	}
	if _, _, err = w.SpendEnvelope(k, "itom-up-data-00", []byte("repeat"), key, charge); err == nil {
		t.Fatal("old session permitted publication")
	}
}
