package journal_test

import (
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func TestLiveLocalPublicationCannotLoseItsObservedRowBeforeCompletion(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "journal.db"))
	defer s.Close()
	enable(t, s)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	g, err := s.Issue(scope, pool.Amount{Bytes: 10000, Messages: 10})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("original physical body before its delayed APPEND tag")
	a, err := s.PrepareStarted(g.Ref, "fast", wire.ID{2}, 1000, body)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.PinLocalPublication(a.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := s.PinLocalPublication(a.Ref); !errors.Is(err, journal.ErrState) {
		t.Fatal("second publisher adopted live attempt", err)
	}
	loc := journal.Location{UIDValidity: 9, UID: 3}
	if err := s.Observe(a.Ref, s.Run(), journal.Observation{Account: scope.Account, Folder: "fast", Location: loc, Decoded: body}); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted(a.Ref, s.Run(), loc); !errors.Is(err, journal.ErrState) {
		t.Fatal("active APPEND lost its completion row", err)
	}
	if err := s.AbsentFromCompleteInventory(a.Ref, s.Run(), "fast"); !errors.Is(err, journal.ErrState) {
		t.Fatal("inventory raced a live writer", err)
	}
	if err := s.Complete(a.Ref, journal.Accepted); err != nil {
		t.Fatal(err)
	}
	if !s.LocalPublicationActive(a.Ref) {
		t.Fatal("journal completion released the lifetime pin early")
	}
	if err := s.Deleted(a.Ref, s.Run(), loc); !errors.Is(err, journal.ErrState) {
		t.Fatal(err)
	}
	release()
	release()
	if s.LocalPublicationActive(a.Ref) {
		t.Fatal("publisher completion did not release its pin")
	}
	if err := s.Deleted(a.Ref, s.Run(), loc); err != nil {
		t.Fatal(err)
	}
	if err := s.Audit(); err != nil {
		t.Fatal(err)
	}
}
