package tunnel

import (
	"context"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func TestDuplexProofSchedulingRetainsPhysicalAuthority(t *testing.T) {
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
	if l.localCleanupThreshold() != 1<<20 {
		t.Fatal("fresh threshold changed")
	}
	old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 3 << 20, Messages: 32})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := s.UsageByOrigin(a.ID, u.ID, l.cipher.Context().Session)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := s.Limit(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	share, _, err := recoveredDirectionalShares(profile, usage)
	if err != nil {
		t.Fatal(err)
	}
	expected := min(uint64(1<<20), max(uint64(64<<10), (share.Bytes-usage.HistoricalLocal.Bytes-localFastReserveBytes)/16))
	for i := 0; i < 32; i++ {
		if got := l.localCleanupThreshold(); got != expected {
			t.Fatal("wrong eligibility threshold", got, expected)
		}
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil || before != after {
		t.Fatal("timer changed held capacity", before, after, err)
	}
	grant, err := s.GetGrant(old.Ref)
	if err != nil || grant != old {
		t.Fatal("timer changed grant", err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
