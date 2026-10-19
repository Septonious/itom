//go:build itomtest

package tunnel

import (
	"fmt"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRC13FieldBytePressureTLSTenAccounts(t *testing.T) {
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "0")
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	old := map[string]journal.Grant{}
	f := newProductionSupervisorFixtureState(t, 10, func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
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
			switch a.ID {
			case "a04":
				amount = pool.Amount{Bytes: 10915159, Messages: 154}
			case "a06":
				amount = pool.Amount{Bytes: 10832733, Messages: 161}
			case "a10":
				amount = pool.Amount{Bytes: 10813503, Messages: 149}
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
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("a%02d", i)
		p := f.openOn(t, id)
		if err := supervisorEcho(p, i); err != nil {
			t.Fatal("account unusable", id, err)
		}
		if id == "a04" || id == "a06" || id == "a10" {
			if err := rev4Exchange(p, 55, 512<<10, 10*time.Second); err != nil {
				t.Fatal("recovered field account bulk unusable", err)
			}
			if err := rev4Exchange(p, 56, 1024, 3*time.Second); err != nil {
				t.Fatal("recovered field account unavailable after bulk", err)
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
	if s := f.client.Snapshot(); s.ActiveLines != 20 {
		t.Fatalf("only %d/20 transport directions", s.ActiveLines)
	}
	t.Log("exact_three_field_accounts_restored accounts=10 active_directions=20 old_debt_unchanged=true sparse_uid_history=64 per_account_TCP_verified=true")
}
