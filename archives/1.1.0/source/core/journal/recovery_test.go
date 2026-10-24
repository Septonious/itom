package journal_test

import (
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func config() journal.Config {
	return journal.Config{Accounts: []journal.Account{{ID: "account1", Provider: "imap.example", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 20, MaxAttempts: 20}
}
func open(t *testing.T, path string) *journal.Store {
	t.Helper()
	s, e := journal.Open(path, config(), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func enable(t *testing.T, s *journal.Store) {
	t.Helper()
	if e := s.EnableAfterInventory("account1", "12345678", s.Run()); e != nil {
		t.Fatal(e)
	}
}
func TestLostAppendReplyRemainsChargedAcrossRestartAndInventory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	s := open(t, path)
	enable(t, s)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	grant, e := s.Issue(scope, pool.Amount{Bytes: 10000, Messages: 10})
	if e != nil {
		t.Fatal(e)
	}
	attempt, e := s.PrepareStarted(grant.Ref, "bulk", wire.ID{2}, 4000, []byte("authenticated physical body"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteAndClose(attempt.Ref, journal.Uncertain); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(attempt.Ref, journal.RejectedBeforeCommit); !errors.Is(e, journal.ErrState) {
		t.Fatal("uncertain outcome refunded as refusal", e)
	}
	s.Close()
	s = open(t, path)
	defer s.Close()
	if _, e = s.Issue(scope, pool.Amount{Bytes: 7000, Messages: 1}); !errors.Is(e, journal.ErrRecovery) {
		t.Fatal("reopen bypassed inventory", e)
	}
	enable(t, s)
	if _, e = s.Issue(scope, pool.Amount{Bytes: 7000, Messages: 1}); !errors.Is(e, journal.ErrLimit) {
		t.Fatal("inventory erased unresolved debt", e)
	}
	old, e := s.GetAttempt(attempt.Ref)
	if e != nil || old.State != journal.Unknown {
		t.Fatal("unknown publication forgotten", old, e)
	}
}
func TestUnownedBodyOrChangedUIDValidityCannotAuthorizeRefund(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "journal.db"))
	defer s.Close()
	enable(t, s)
	g, e := s.Issue(journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 10000, Messages: 10})
	if e != nil {
		t.Fatal(e)
	}
	body := []byte("authenticated body")
	a, e := s.PrepareStarted(g.Ref, "bulk", wire.ID{2}, 4000, body)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(a.Ref, journal.Uncertain); e != nil {
		t.Fatal(e)
	}
	proof := journal.Observation{Account: "account1", Folder: "bulk", EncodedBytes: 4000, Location: journal.Location{UIDValidity: 4, UID: 9}, Decoded: []byte("different body")}
	if e = s.Observe(a.Ref, s.Run(), proof); e == nil {
		t.Fatal("wrong body accepted")
	}
	proof.Decoded = body
	if e = s.Observe(a.Ref, s.Run(), proof); e != nil {
		t.Fatal(e)
	}
	if e = s.Deleted(a.Ref, s.Run(), journal.Location{UIDValidity: 5, UID: 9}); e == nil {
		t.Fatal("UIDVALIDITY change refunded old body")
	}
}
