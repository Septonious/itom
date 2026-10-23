package tunnel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// DEV5 field a08 has no current grant: all 11,193,823 bytes / 165 slots
// remain charged. a02/a13 also hold one current 512 KiB fast lease, removed
// here to reproduce their pre-offer byte headroom. These are sufficient
// debt shapes, not claims that the captures contain the complete journal.
var dev6ByteDebt = []pool.Amount{
	{Bytes: 10976200, Messages: 156},
	{Bytes: 11193823, Messages: 165},
	{Bytes: 10780973, Messages: 173},
}

func TestDEV6FieldByteHeadroomBootstrapsWithoutForgivingDebt(t *testing.T) {
	for i, debt := range dev6ByteDebt {
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
			local, err := l.tryLocalGrant(dataBatchMIMEHardCap, false)
			if err != nil {
				t.Fatal("maximum outbound bulk BODY stranded", err)
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

func TestDEV6ByteHeadroomTLSFifteenAccounts(t *testing.T) {
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "0")
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	// Two client sockets and three server sockets per account, as captured.
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "5")
	t.Setenv("ITOM_LOAD_ENDPOINT_CONNECTIONS", "45")
	started := time.Now()
	old := map[string]journal.Grant{}
	f := newProductionSupervisorFixtureState(t, 15, func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
		for i, a := range cfg.Accounts {
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
			// All fifteen accounts exercise the severe observed byte-pressure shapes.
			grant, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, dev6ByteDebt[i%len(dev6ByteDebt)])
			if err != nil {
				t.Fatal(err)
			}
			old[a.ID] = grant
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	readyAfter := time.Since(started)
	t.Logf("all_ready_ms=%d accounts=15 before_application_traffic=true", readyAfter.Milliseconds())
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("a%02d", i)
		p := f.openOn(t, id)
		if err := supervisorEcho(p, i); err != nil {
			t.Fatal(id, "short DATA failed", err)
		}
		if err := rev4Exchange(p, 100+i, 512<<10, 15*time.Second); err != nil {
			t.Fatal(id, "bulk DATA failed", err)
		}
		if err := supervisorEcho(p, 200+i); err != nil {
			t.Fatal(id, "post-bulk short DATA failed", err)
		}
		f.mu.Lock()
		server := f.serverLanes[id]
		f.mu.Unlock()
		got, err := server.data.GetGrant(old[id].Ref)
		if err != nil || got != old[id] {
			t.Fatal(id, "old debt changed", err)
		}
		if err = server.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	if s := f.client.Snapshot(); s.ActiveLines != 30 {
		t.Fatalf("only %d/30 active directions", s.ActiveLines)
	}
	t.Logf("byte_pressure_accounts=15 active_directions=30 all_ready_ms=%d per_account_short_bulk_short=true old_debt_unchanged=true", readyAfter.Milliseconds())
}
