package tunnel

import (
	"context"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRev7FullJournalWaitsForRealCapacityWithoutSelfWake(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.EnableAfterInventory(a.ID, u.ID, s.Run())
	full, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, dataProfile12)
	if err != nil {
		t.Fatal(err)
	}
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, a.ID, true)
	defer l.cancel()
	l.stripe = g
	l.account = a
	l.user = u
	l.data = s
	l.cipher = cleanupFixtureEpoch(t)
	g.lanes[a.ID] = l
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wakes atomic.Int64
	go func() {
		for {
			ch := g.capacityNotice.watch()
			select {
			case <-ch:
				wakes.Add(1)
			case <-ctx.Done():
				return
			}
		}
	}()
	type result struct {
		lane  *lane
		grant journal.Grant
		err   error
	}
	done := make(chan result, 1)
	go func() {
		carrier, grant, e := g.takeLocalGrantForClass(ctx, nil, false, dataBatchMIMEHardCap)
		done <- result{carrier, grant, e}
	}()
	time.Sleep(100 * time.Millisecond)
	if n := wakes.Load(); n != 0 {
		t.Fatalf("failed quota probes manufactured %d capacity notifications without freeing anything", n)
	}
	// A signed unused-grant fence really does return permission. Wake exactly
	// that capacity transition and require the original waiter to make progress.
	key := wire.QuotaKey{Store: full.Ref.Store, Grant: full.Ref.ID, Epoch: full.Run, Session: full.Ref.Scope.Session, Account: a.ID, User: u.ID}
	if _, err = s.ApplyRemote(u.ID, s.Run(), wire.StorageRelease{Key: key}); err != nil {
		t.Fatal(err)
	}
	g.signal()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if err = r.lane.releaseReservedLocalGrant(r.grant); err != nil {
			t.Fatal(err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("real returned quota did not wake the blocked writer")
	}
}
