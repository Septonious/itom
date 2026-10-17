package datapath

import (
	"context"
	"errors"
	"io"
	"itom.local/core/flow"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

type appendFunc func(context.Context, string, []byte) error

func (f appendFunc) Append(ctx context.Context, folder string, body []byte) error {
	return f(ctx, folder, body)
}

type writerFixture struct {
	e      *Engine
	ledger *journal.Store
	wallet *wallet.Wallet
	cipher *secure.Epoch
	remote wire.StorageGrant
	local  journal.GrantRef
}

func writers(t *testing.T, budget uint64) writerFixture {
	t.Helper()
	cfg := journal.Config{MaxGrants: 16, MaxAttempts: 32, Accounts: []journal.Account{{ID: "a01", Provider: "test", Limit: pool.Amount{Bytes: 4 << 20, Messages: 64}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 4 << 20, Messages: 64}, Folders: []string{"u-up", "u-down"}}}}}}
	s, err := journal.Open(filepath.Join(t.TempDir(), "ledger.db"), cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.EnableAfterInventory("a01", "12345678", s.Run()); err != nil {
		t.Fatal(err)
	}
	w, err := wallet.Open(filepath.Join(t.TempDir(), "wallet.db"), "12345678", s.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	scope := journal.Scope{Account: "a01", User: "12345678", Session: w.Session()}
	g, err := s.IssueRemote(scope, pool.Amount{Bytes: budget, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	local, err := s.Issue(scope, pool.Amount{Bytes: budget, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := epochs(t, w.Session())
	e, _ := pair(t)
	return writerFixture{e, s, w, cipher, offer, local.Ref}
}
func (f writerFixture) job(t *testing.T, remote bool) *Job {
	t.Helper()
	id, err := f.e.Queue(1, 1, []byte("retained despite lost APPEND reply"))
	if err != nil {
		t.Fatal(err)
	}
	frame, err := f.e.Frame(1, id)
	if err != nil {
		t.Fatal(err)
	}
	var key *wire.QuotaKey
	if remote {
		key = &f.remote.Key
	}
	n, err := Size(frame, key)
	if err != nil {
		t.Fatal(err)
	}
	j, err := f.e.Begin(1, id, "a01", n, 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
func TestRemoteUnknownRetainsQuotaAndOnePhysicalAttempt(t *testing.T) {
	f := writers(t, 1<<20)
	j := f.job(t, true)
	copyJob := *j
	calls := 0
	appendUnknown := appendFunc(func(context.Context, string, []byte) error { calls++; return io.ErrUnexpectedEOF })
	r := j.WriteRemote(f.wallet, f.remote.Key, "u-up", f.cipher, appendUnknown)
	if r.Outcome != flow.CommitUnknown || r.Usage == nil || !errors.Is(r.Err, io.ErrUnexpectedEOF) {
		t.Fatal(r)
	}
	if err := f.e.Complete(r.Generation, r.Attempt, r.Outcome); err != nil {
		t.Fatal(err)
	}
	if r2 := copyJob.WriteRemote(f.wallet, f.remote.Key, "u-up", f.cipher, appendUnknown); r2.Err == nil || calls != 1 {
		t.Fatal("permit reused", r2, calls)
	}
	entries, err := f.wallet.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatal(err)
	}
	if entries[0].Used.Messages != 1 || entries[0].Used.Bytes != j.Attempt().EncodedBytes || len(entries[0].Pending) != 1 {
		t.Fatal(entries)
	}
	tx, _ := f.e.Snapshot()
	if tx.Logical == 0 || tx.Attempts != 1 {
		t.Fatal("uncertainty freed debt", tx)
	}
}
func TestLocalUnknownRetainsJournalCharge(t *testing.T) {
	f := writers(t, 1<<20)
	j := f.job(t, false)
	r := j.WriteLocal(f.ledger, f.local, "u-down", f.cipher, appendFunc(func(context.Context, string, []byte) error { return io.ErrUnexpectedEOF }))
	if r.Outcome != flow.CommitUnknown || r.Local == nil || !errors.Is(r.Err, io.ErrUnexpectedEOF) {
		t.Fatal(r)
	}
	a, err := f.ledger.GetAttempt(*r.Local)
	if err != nil || a.State != journal.Unknown {
		t.Fatal(a, err)
	}
	g, err := f.ledger.GetGrant(f.local)
	if err != nil || g.Used.Messages != 1 || g.Released.Messages != 0 {
		t.Fatal(g, err)
	}
}
func TestNoAppendWithoutBudgetMatchingBindingAndLiveGeneration(t *testing.T) {
	for _, which := range []string{"remote-budget", "local-budget", "binding", "cancelled"} {
		t.Run(which, func(t *testing.T) {
			budget := uint64(1 << 20)
			if which == "remote-budget" || which == "local-budget" {
				budget = 8
			}
			f := writers(t, budget)
			remote := which != "local-budget"
			j := f.job(t, remote)
			if which == "binding" {
				f.cipher, _ = epochs(t, wire.ID{99})
			}
			if which == "cancelled" {
				f.e.Stop()
			}
			calls := 0
			app := appendFunc(func(context.Context, string, []byte) error { calls++; return nil })
			var r Completion
			if remote {
				r = j.WriteRemote(f.wallet, f.remote.Key, "u-up", f.cipher, app)
			} else {
				r = j.WriteLocal(f.ledger, f.local, "u-down", f.cipher, app)
			}
			if r.Err == nil || calls != 0 || r.Usage != nil || r.Local != nil {
				t.Fatal("unauthorized physical write", r, calls)
			}
			entries, err := f.wallet.Entries()
			if err != nil || len(entries) != 1 || entries[0].Used.Messages != 0 {
				t.Fatal(entries, err)
			}
			g, err := f.ledger.GetGrant(f.local)
			if err != nil || g.Used.Messages != 0 {
				t.Fatal(g, err)
			}
		})
	}
}

func TestRemoteDescriptorMustMatchSessionBeforeDecrypt(t *testing.T) {
	f := writers(t, 1<<20)
	j := f.job(t, true)
	var raw []byte
	r := j.WriteRemote(f.wallet, f.remote.Key, "u-up", f.cipher, appendFunc(func(_ context.Context, _ string, b []byte) error { raw = append([]byte(nil), b...); return nil }))
	if r.Err != nil || r.Usage == nil {
		t.Fatal(r)
	}
	// A different epoch with the same public session is not the correct peer;
	// this case must reject at the outer authenticated descriptor boundary.
	_, rx := epochs(t, f.wallet.Session())
	changed := *r.Usage
	changed.Key.User = "87654321"
	if _, err := OpenRemote(raw, changed, rx); !errors.Is(err, ErrState) {
		t.Fatal("cross-user descriptor accepted", err)
	}
	changed = *r.Usage
	changed.Key.Session = wire.ID{98}
	if _, err := OpenRemote(raw, changed, rx); !errors.Is(err, ErrState) {
		t.Fatal("cross-session descriptor accepted", err)
	}
}
