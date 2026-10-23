package tunnel

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestDEV9LatestFieldDebtKeepsDuplexServiceWithoutRefund(t *testing.T) {
	for i, debt := range []pool.Amount{{Bytes: 11329548, Messages: 166}} {
		t.Run(fmt.Sprintf("shape-%d", i), func(t *testing.T) {
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
			s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, debt)
			if err != nil {
				t.Fatal(err)
			}
			l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
			establishRecoveredServer(t, l)
			if err = l.offer(); err != nil {
				t.Fatal(err)
			}
			fast, bulk := l.issuedGrantCounts()
			if !l.ready.Load() || fast != 1 || bulk < 1 {
				t.Fatalf("byte headroom stranded: debt=%+v ready=%t fast=%d bulk=%d", debt, l.ready.Load(), fast, bulk)
			}
			// Hold a full bulk BODY and a worst-case fast BODY concurrently. Neither
			// inbound allocation may take the outbound service classes' real capacity.
			local, err := l.tryLocalGrant(448<<10, false)
			if err != nil {
				t.Fatal("bounded outbound bulk BODY stranded", err)
			}
			short, err := l.tryLocalGrant(160<<10, true)
			if err != nil {
				t.Fatal("fast cushion lost alongside bulk", err)
			}
			for _, g := range []journal.Grant{local, short} {
				if err = s.CloseGrant(g.Ref); err != nil {
					t.Fatal(err)
				}
			}
			// Repeated authenticated zero-spend fences must refill fast service
			// while the bulk lease remains outstanding. Its already-charged
			// bytes/ordinals must not be reserved for a second time.
			for round := 0; round < 3; round++ {
				for key, active := range l.issued {
					if active && l.issuedFast[key] {
						if _, err = s.ApplyRemote(u.ID, s.Run(), wire.StorageRelease{Key: key}); err != nil {
							t.Fatal(err)
						}
						l.issued[key] = false
					}
				}
				if err = l.offer(); err != nil {
					t.Fatal(err)
				}
				if fast, bulk := l.issuedGrantCounts(); fast != 1 || bulk < 1 {
					t.Fatalf("fast refill stranded behind existing bulk: round=%d fast=%d bulk=%d", round, fast, bulk)
				}
			}
			got, err := s.GetGrant(old.Ref)
			if err != nil || got != old {
				t.Fatal("historical debt changed", err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
