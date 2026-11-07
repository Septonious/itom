package journal_test

import (
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func localKey(g journal.Grant) wire.QuotaKey {
	return wire.QuotaKey{Store: g.Ref.Store, Grant: g.Ref.ID, Epoch: g.Run, Account: g.Ref.Scope.Account, User: g.Ref.Scope.User, Session: g.Ref.Scope.Session}
}

func TestLocalBodyCorrelationSurvivesRestartWithoutReleasingPhysicalDebt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "issuer.db")
	s := open(t, path)
	enable(t, s)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	g, err := s.Issue(scope, pool.Amount{Bytes: 10000, Messages: 10})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("exact original body")
	a, err := s.PrepareStarted(g.Ref, "bulk", wire.ID{2}, 4000, body)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteAndClose(a.Ref, journal.Uncertain); err != nil {
		t.Fatal(err)
	}
	k := localKey(g)
	ref, err := s.LocalAttempt(k, a.Batch)
	if err != nil || ref != a.Ref {
		t.Fatal(ref, err)
	}
	s.Close()
	s = open(t, path)
	defer s.Close()
	ref, err = s.LocalAttempt(k, a.Batch)
	if err != nil || ref != a.Ref {
		t.Fatal("original index not recovered", ref, err)
	}
	for _, change := range []func(*wire.QuotaKey){func(k *wire.QuotaKey) { k.Store[0]++ }, func(k *wire.QuotaKey) { k.Epoch++ }, func(k *wire.QuotaKey) { k.Account = "other" }, func(k *wire.QuotaKey) { k.User = "87654321" }, func(k *wire.QuotaKey) { k.Session[0]++ }} {
		wrong := k
		change(&wrong)
		if _, err = s.LocalAttempt(wrong, a.Batch); err == nil {
			t.Fatal("foreign descriptor correlated", wrong)
		}
	}
	if _, err = s.LocalAttempt(k, wire.ID{3}); !errors.Is(err, journal.ErrMissing) {
		t.Fatal(err)
	}
	held, err := s.Held(scope.Account, scope.User)
	if err != nil || held.Bytes != 4000 || held.Messages != 1 {
		t.Fatal("lookup released physical exposure", held, err)
	}
	proof := journal.Observation{Account: scope.Account, Folder: "bulk", EncodedBytes: 4000, Location: journal.Location{UIDValidity: 9, UID: 17}, Decoded: []byte("forged body")}
	if err = s.Observe(ref, s.Run(), proof); err == nil {
		t.Fatal("lookup replaced full BODY proof")
	}
	proof.Decoded = body
	if err = s.Observe(ref, s.Run(), proof); err != nil {
		t.Fatal(err)
	}
	held, err = s.Held(scope.Account, scope.User)
	if err != nil || held.Bytes != 4000 {
		t.Fatal("observation refunded debt", held, err)
	}
	if err = s.Deleted(ref, s.Run(), proof.Location); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LocalAttempt(k, a.Batch); !errors.Is(err, journal.ErrMissing) {
		t.Fatal("deleted attempt retained index", err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalBodyCorrelationRejectsAmbiguousBatchesAndRemoteGrants(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "issuer.db"))
	defer s.Close()
	enable(t, s)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	g, err := s.Issue(scope, pool.Amount{Bytes: 5000, Messages: 5})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = s.PrepareStarted(g.Ref, "fast", wire.ID{2}, 1000, []byte("same batch, ambiguous originals")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.LocalAttempt(localKey(g), wire.ID{2}); !errors.Is(err, journal.ErrScope) {
		t.Fatal("ambiguous body chose arbitrary attempt", err)
	}
	r, err := s.IssueRemote(scope, pool.Amount{Bytes: 1000, Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LocalAttempt(localKey(r), wire.ID{2}); !errors.Is(err, journal.ErrScope) {
		t.Fatal("remote authority accepted as local", err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
