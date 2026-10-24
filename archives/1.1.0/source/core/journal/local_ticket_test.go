package journal_test

import (
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"sync"
	"testing"
)

func TestLocalAdmissionCannotBorrowRetainedRemoteReservationOrRefundStartedBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	s := open(t, path)
	enable(t, s)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	remote, err := s.IssueRemote(scope, pool.Amount{Bytes: 6000, Messages: 6})
	if err != nil {
		t.Fatal(err)
	}
	size := func(n int) (uint64, error) { return uint64(n + 1000), nil }
	ticket, quantum, err := s.ReserveLocalSized(scope, "bulk", 1, 5000, size)
	if err != nil || quantum != 3000 || ticket.MaxEncoded != 4000 {
		t.Fatal("local ticket ignored retained remote allocation", quantum, err)
	}
	if _, _, err = s.ReserveLocalSized(scope, "bulk", 1, 10, size); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("overlapping local reservation borrowed held quota", err)
	}
	attempt, err := ticket.Commit(wire.ID{2}, 3500, []byte("owned server body"))
	if err != nil {
		t.Fatal(err)
	}
	if err = ticket.Cancel(); err != nil {
		t.Fatal(err)
	}
	held, err := s.Held(scope.Account, scope.User)
	if err != nil || held != (pool.Amount{Bytes: 9500, Messages: 7}) {
		t.Fatal("cancel released started physical debt or retained unused allocation", held, err)
	}
	if err = s.Complete(attempt.Ref, journal.Uncertain); err != nil {
		t.Fatal(err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = open(t, path)
	defer s.Close()
	enable(t, s)
	if held, err = s.Held(scope.Account, scope.User); err != nil || held.Bytes != 9500 {
		t.Fatal("restart lost local/remote held allocation", held, err)
	}
	actualRemote, err := s.GetGrant(remote.Ref)
	if err != nil || actualRemote.Used != (pool.Amount{}) || actualRemote.Limit.Bytes != 6000 {
		t.Fatal("local work modified unproved remote history", err)
	}
	if _, _, err = s.ReserveLocalSized(scope, "bulk", 1, 100, size); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("fresh process invented available capacity", err)
	}
}

func TestLocalTicketHasOneWriterAndCanceledUnusedTicketCannotCommit(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "journal.db"))
	defer s.Close()
	enable(t, s)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	size := func(n int) (uint64, error) { return uint64(n + 1000), nil }
	ticket, _, err := s.ReserveLocalSized(scope, "fast", 1, 100, size)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := ticket.Commit(wire.ID{2}, 1050, []byte("one physical body"))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, journal.ErrState) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || rejected != 1 {
		t.Fatal("one ticket granted multiple writers", success, rejected)
	}
	unused, _, err := s.ReserveLocalSized(scope, "fast", 1, 100, size)
	if err != nil {
		t.Fatal(err)
	}
	if err = unused.Cancel(); err != nil {
		t.Fatal(err)
	}
	if _, err = unused.Commit(wire.ID{3}, 1050, []byte("must not start")); !errors.Is(err, journal.ErrState) {
		t.Fatal("canceled unused ticket authorized I/O", err)
	}
	if held, err := s.Held(scope.Account, scope.User); err != nil || held != (pool.Amount{Bytes: 1050, Messages: 1}) {
		t.Fatal("ticket lifecycle changed physical debt", held, err)
	}
}
