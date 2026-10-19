package tunnel

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func rc13RetainedLane(t *testing.T, debt pool.Amount) (*lane, *journal.Store, journal.Grant) {
	t.Helper()
	a := servercfg.Account{ID: "a01"}
	a.Mail.Host = "mail.example"
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	var old journal.Grant
	if debt.Bytes > 0 || debt.Messages > 0 {
		old, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, debt)
		if err != nil {
			t.Fatal(err)
		}
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 128), controlQueue: make(chan queuedControl, 128), maintenanceQueue: make(chan queuedControl, 128)}
	establishRecoveredServer(t, l)
	return l, s, old
}

// Exact pre-offer retained charges from the three RC12 accounts that never
// reached ready. All old debt stays charged; both live directions need room.
func TestRC13FieldBytePressureBootstrap(t *testing.T) {
	for _, debt := range []pool.Amount{{Bytes: 10915159, Messages: 154}, {Bytes: 10832733, Messages: 161}, {Bytes: 10813503, Messages: 149}} {
		t.Run(fmt.Sprint(debt.Bytes), func(t *testing.T) {
			l, s, old := rc13RetainedLane(t, debt)
			if err := l.offer(); err != nil {
				t.Fatal(err)
			}
			fast, bulk := l.issuedGrantCounts()
			if !l.ready.Load() || fast != 1 || bulk < 1 {
				t.Fatalf("field account stranded: ready=%t fast=%d bulk=%d debt=%+v", l.ready.Load(), fast, bulk, debt)
			}
			g, err := l.tryLocalGrant(dataBatchMIMEHardCap, false)
			if err != nil {
				t.Fatal("no full local bulk BODY headroom", err)
			}
			bulkGrant := g
			g, err = l.tryLocalGrant(128<<10, true)
			if err != nil {
				t.Fatal("no bounded local fast headroom", err)
			}
			if err = s.CloseGrant(g.Ref); err != nil {
				t.Fatal(err)
			}
			if err = s.CloseGrant(bulkGrant.Ref); err != nil {
				t.Fatal(err)
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
