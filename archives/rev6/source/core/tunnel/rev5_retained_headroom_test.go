package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// The paired 15 October captures show a05 fixed at 6,571,914 bytes and
// 180/192 ordinals. Twelve unused ordinals must serve both duplex classes;
// retaining an arbitrary twelve-ordinal local cushion is not a full ledger.
func TestRev5Historical180SlotsBootstrapWithoutForgivingDebt(t *testing.T) {
	a := servercfg.Account{ID: "a05"}
	a.Mail.Host = "mail.example"
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6571914, Messages: 180})
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t),
		stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{},
		urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
	establishRecoveredServer(t, l)
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	fast, bulk := l.issuedGrantCounts()
	if !l.ready.Load() || fast != 1 || bulk < 1 {
		t.Fatalf("field a05 stranded with 12 unused ordinals: ready=%t fast=%d bulk=%d", l.ready.Load(), fast, bulk)
	}
	for _, fast := range []bool{false, true} {
		g, err := l.tryLocalGrant(dataBatchMIMEHardCap, fast)
		if err != nil {
			t.Fatalf("local class fast=%t lost all headroom: %v", fast, err)
		}
		if err = s.CloseGrant(g.Ref); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetGrant(old.Ref)
	if err != nil || got != old {
		t.Fatal("historical reservation changed without proof", got, err)
	}
	held, err := s.Held(a.ID, u.ID)
	if err != nil || held.Messages > dataProfile12.Messages-2 || held.Bytes > dataProfile12.Bytes-localFastReserveBytes-dataBatchMIMEHardCap {
		t.Fatal("duplex reserves or physical cap violated", held, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

// Actual production supervisors with fifteen TLS/IMAP accounts and a fresh
// client wallet reproduce the blocked field account, not only an offer unit.
func TestRev5Historical180SlotsTLSFifteenAccounts(t *testing.T) {
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "0")
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	old := map[string]journal.Grant{}
	f := newProductionSupervisorFixtureState(t, 15, func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
		for _, a := range cfg.Accounts {
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			amount := pool.Amount{Bytes: 6 << 20, Messages: 130}
			if a.ID == "a05" {
				amount = pool.Amount{Bytes: 6571914, Messages: 180}
			}
			g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, amount)
			if err != nil {
				t.Fatal(err)
			}
			old[a.ID] = g
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("a%02d", i)
		p := f.openOn(t, id)
		if err := supervisorEcho(p, i); err != nil {
			t.Fatal("account unusable", id, err)
		}
		if id == "a05" {
			if err := rev4Exchange(p, 55, 512<<10, 10*time.Second); err != nil {
				t.Fatal("recovered a05 bulk unusable", err)
			}
			if err := rev4Exchange(p, 56, 1024, 3*time.Second); err != nil {
				t.Fatal("recovered a05 unavailable after bulk", err)
			}
		}
		f.mu.Lock()
		server := f.serverLanes[id]
		f.mu.Unlock()
		g, err := server.data.GetGrant(old[id].Ref)
		if err != nil || g != old[id] {
			t.Fatal("old allocation changed during real traffic", id, err)
		}
		if err = server.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	if s := f.client.Snapshot(); s.ActiveLines != 30 {
		t.Fatalf("only %d/30 transport directions", s.ActiveLines)
	}
	t.Log("field_account_a05_old_bytes=6571914 old_messages=180 accounts=15 active_directions=30 old_debt_unchanged=true sparse_uid_history=64 per_account_TCP_verified=true")
}
