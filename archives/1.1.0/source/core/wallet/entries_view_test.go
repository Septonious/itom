package wallet_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"itom.local/core/wire"
)

func TestPolicyEntryViewCannotForgetCommittedSpendOrAliasCallerMutations(t *testing.T) {
	w := walletAt(t, filepath.Join(t.TempDir(), "wallet.db"))
	defer w.Close()
	g := offer(w, 1, 20000, 10)
	if err := w.Accept(g); err != nil {
		t.Fatal(err)
	}
	ticket, err := w.Reserve(4000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ticket.Commit("fast", []byte("sealed bytes"), bytes.Repeat([]byte{2}, 32), encoded); err != nil {
		t.Fatal(err)
	}
	first, err := w.Entries()
	if err != nil || len(first) != 1 || len(first[0].Pending) != 1 {
		t.Fatal(first, err)
	}
	descriptor := *first[0].Pending[0].Attempt
	first[0].Used = wire.Quota{}
	first[0].Pending[0].Attempt.Folder = "foreign"
	first[0].Pending[0].Total = wire.Quota{}
	again, err := w.Entries()
	if err != nil || again[0].Used.Messages != 1 || *again[0].Pending[0].Attempt != descriptor || again[0].Pending[0].Total.Messages != 1 {
		t.Fatal("caller mutated inspection cache", again, err)
	}
	ticket, err = w.Reserve(4000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ticket.Commit("fast", []byte("different sealed bytes"), bytes.Repeat([]byte{2}, 32), encoded); err != nil {
		t.Fatal(err)
	}
	current, err := w.Entries()
	if err != nil || current[0].Used.Messages != 2 || len(current[0].Pending) != 2 || again[0].Used.Messages != 1 {
		t.Fatal("committed spend did not invalidate snapshot", current, err)
	}
	if _, err = w.Fence(g.Key); err != nil {
		t.Fatal(err)
	}
	current, err = w.Entries()
	if err != nil || !current[0].Fenced || current[0].Used.Messages != 2 {
		t.Fatal("committed fence/spend missing", current, err)
	}
}
