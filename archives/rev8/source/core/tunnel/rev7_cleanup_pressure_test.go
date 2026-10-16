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

func TestRev7RetainedOutboundCleanupStartsBeforeSmallWindowIsFull(t *testing.T) {
	for _, retained := range []bool{false, true} {
		name := "fresh"
		if retained {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
			s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.EnableAfterInventory(a.ID, u.ID, s.Run())
			if retained {
				if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: 130}); err != nil {
					t.Fatal(err)
				}
			}
			l := &lane{ctx: context.Background(), side: true, stripe: newStripeGroup(wire.ID{1}, 1), account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t)}
			scope := journal.Scope{Account: a.ID, User: u.ID, Session: l.cipher.Context().Session}
			for i := 0; i < 2; i++ {
				if _, err = s.Issue(scope, pool.Amount{Bytes: 480 << 10, Messages: 1}); err != nil {
					t.Fatal(err)
				}
				want := retained && i == 1
				if got := l.ownerCleanupPressure(cleanupLocalData); got != want {
					t.Fatalf("cleanup pressure retained=%t body=%d got=%t want=%t", retained, i+1, got, want)
				}
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
