package wallet

import (
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func TestSpendDedupFenceRestartAndFinalAck(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wallet.db")
	store := wire.ID{1}
	w, e := Open(p, "12345678", store)
	if e != nil {
		t.Fatal(e)
	}
	o := wire.StorageGrant{Key: wire.QuotaKey{Store: store, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}, Limit: wire.Quota{Bytes: 10000, Messages: 10}}
	if e = w.Accept(o); e != nil {
		t.Fatal(e)
	}
	r, body, e := w.Spend(o.Key, "itom-up", []byte("ciphertext"), func(b []byte) (uint64, error) { return uint64(len(b) + 100), nil })
	if e != nil || !wire.CheckPhysical(body, o.Key, 1, r.Attempt.Batch) {
		t.Fatal(e)
	}
	if e = w.Accept(o); e != nil {
		t.Fatal(e)
	}
	rows, e := w.Entries()
	if e != nil || rows[0].Used != r.Total {
		t.Fatal(e)
	}
	ack := wire.CleanupStatus{Key: o.Key, Used: r.Total, Registered: r.Total}
	if e = w.Acknowledge(ack); e != nil {
		t.Fatal(e)
	}
	f, e := w.Fence(o.Key)
	if e != nil || f.Final != r.Total {
		t.Fatal(e)
	}
	if _, _, e = w.Spend(o.Key, "itom-up", []byte("x"), func([]byte) (uint64, error) { return 300, nil }); e != ErrState {
		t.Fatal(e)
	}
	ack.Closed = true
	ack.Released = r.Total
	if e = w.Acknowledge(ack); e != nil {
		t.Fatal(e)
	}
	retire, e := w.Retirements()
	if e != nil || len(retire) != 1 {
		t.Fatal(retire, e)
	}
	retire[0].Ack = true
	if e = w.CompleteRetirement(retire[0]); e != nil {
		t.Fatal(e)
	}
	rows, e = w.Entries()
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	if e = w.Accept(o); e != ErrState {
		t.Fatal("retired replay", e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	w, e = Open(p, "12345678", store)
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	if e = w.Accept(o); e != ErrState {
		t.Fatal(e)
	}
}
func TestRestartRetainsDescriptorsAndFencesOldSession(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wallet.db")
	w, e := Open(p, "12345678", wire.ID{1})
	if e != nil {
		t.Fatal(e)
	}
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}
	o := wire.StorageGrant{Key: k, Limit: wire.Quota{Bytes: 10000, Messages: 10}}
	if e = w.Accept(o); e != nil {
		t.Fatal(e)
	}
	r, _, e := w.Spend(k, "itom-up", []byte("cipher"), func([]byte) (uint64, error) { return 1000, nil })
	if e != nil {
		t.Fatal(e)
	}
	w.Close()
	w, e = Open(p, "12345678", wire.ID{1})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	rows, e := w.Entries()
	if e != nil || !rows[0].Fenced || rows[0].Pending[0].Total != r.Total || w.Session() == k.Session {
		t.Fatal(rows, e)
	}
	if e = w.Accept(o); e != nil {
		t.Fatal(e)
	}
	f, e := w.Fence(k)
	if e != nil || f.Final != r.Total {
		t.Fatal(e)
	}
	if _, _, e = w.Spend(k, "itom-up", []byte("cipher"), func([]byte) (uint64, error) { return 1000, nil }); e != ErrState {
		t.Fatal(e)
	}
}
func TestWalletRejectsCrossScopeAndConflictingGrant(t *testing.T) {
	w, e := Open(filepath.Join(t.TempDir(), "w.db"), "12345678", wire.ID{1})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	o := wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a"}, Limit: wire.Quota{Bytes: 1000, Messages: 1}}
	bad := o
	bad.Key.User = "87654321"
	if e = w.Accept(bad); e != ErrState {
		t.Fatal(e)
	}
	if e = w.Accept(o); e != nil {
		t.Fatal(e)
	}
	bad = o
	bad.Limit.Bytes++
	if e = w.Accept(bad); e != ErrState {
		t.Fatal(e)
	}
	if _, _, e = w.Spend(o.Key, "itom-up", []byte("x"), func([]byte) (uint64, error) { return 2000, nil }); e != ErrExhausted {
		t.Fatal(e)
	}
	rows, _ := w.Entries()
	if rows[0].Used.Messages != 0 {
		t.Fatal("failed spend changed usage")
	}
}

func TestAcknowledgeDistinguishesMissingGrantFromStateDivergence(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "w.db"), "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	missing := wire.CleanupStatus{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 999, Session: w.Session(), User: "12345678", Account: "a01"}, Closed: true}
	if err = w.Acknowledge(missing); err != ErrMissing {
		t.Fatalf("missing grant classification=%v", err)
	}
	o := wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}, Limit: wire.Quota{Bytes: 1024, Messages: 2}}
	if err = w.Accept(o); err != nil {
		t.Fatal(err)
	}
	bad := wire.CleanupStatus{Key: o.Key, Used: wire.Quota{Bytes: 1, Messages: 1}, Closed: true}
	if err = w.Acknowledge(bad); err != ErrState {
		t.Fatalf("divergent existing grant classification=%v", err)
	}
}
