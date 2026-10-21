package tunnel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestRetainedCadenceCannotAlterHistoricalDebtOrCreatePermissions(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile8), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newModelStripeGroup(wire.ID{1}, 1)}
	if l.remoteCleanupPeriod() != runtimeRemoteCleanupInterval {
		t.Fatal("fresh profile changed cleanup cadence")
	}
	old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 3 << 20, Messages: 32})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		period := l.remoteCleanupPeriod()
		if period < 900*time.Millisecond || period >= runtimeRemoteCleanupInterval {
			t.Fatal("retained headroom did not produce bounded earlier proof work", period)
		}
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil || after != before {
		t.Fatal("scheduling changed physical exposure", before, after, err)
	}
	unchanged, err := s.GetGrant(old.Ref)
	if err != nil || unchanged != old {
		t.Fatal("scheduling changed old permission without proof", old, unchanged, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
