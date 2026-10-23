//go:build itomtest

package tunnel

import (
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Latest captured capacity, including a08's 1,253,364 free bytes. Reopening a
// real journal and missing client history must not invent a spend or refund.
func TestDEV9LatestFieldReadiness(t *testing.T) {
	t.Setenv("ITOM_LOAD_DEV7_FIELD_DEBT", "1")
	t.Setenv("ITOM_LOAD_DEV9_FIELD_DEBT", "1")
	t.Setenv("ITOM_LOAD_MANY_RETAINED_GRANTS", "1")
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "6")
	t.Setenv("ITOM_LOAD_CLIENT_ACCOUNT_SESSIONS", "4")
	t.Setenv("ITOM_LOAD_ENDPOINT_CONNECTIONS", "45")
	f := newProductionSupervisorFixtureState(t, 15, fieldSequenceHistory(t))
	snap := f.client.Snapshot()
	if snap.ActiveLines != 30 {
		t.Fatalf("only %d/30 directions ready", snap.ActiveLines)
	}
	f.mu.Lock()
	l := f.serverLanes["a08"]
	f.mu.Unlock()
	u, e := l.data.UsageByOrigin(l.account.ID, l.user.ID, l.cipher.Context().Session)
	if e != nil || u.HistoricalRemote != (pool.Amount{Bytes: 11329548, Messages: 166}) {
		t.Fatalf("historical charge changed: %+v err=%v", u.HistoricalRemote, e)
	}
	if e = l.data.Audit(); e != nil {
		t.Fatal(e)
	}
	t.Logf("dev9_readiness variant=%s active=30 target=30 historical_charge_unchanged=true", os.Getenv("ITOM_EXPERIMENT_LABEL"))
}

// Exercise actual bulk in each direction on the smaller grant, not just a
// readiness bit. A single indebted account cannot hide behind healthy siblings.
func TestDEV9TightAccountTransfersBothDirections(t *testing.T) {
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "6")
	t.Setenv("ITOM_LOAD_CLIENT_ACCOUNT_SESSIONS", "4")
	f := newProductionSupervisorFixtureState(t, 1, func(serverDir, _ string, c servercfg.Config, u servercfg.User) {
		a := c.Accounts[0]
		dir := filepath.Join(serverDir, a.ID, u.ID)
		if e := os.MkdirAll(dir, 0700); e != nil {
			t.Fatal(e)
		}
		s, e := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.EnableAfterInventory(a.ID, u.ID, s.Run()); e != nil {
			t.Fatal(e)
		}
		if _, e = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 11329548, Messages: 166}); e != nil {
			t.Fatal(e)
		}
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
	})
	p := rev4Open(t, f)
	defer p.Close()
	if e := rev4Exchange(p, 123, 512<<10, 5*time.Second); e != nil {
		t.Fatal("bounded bulk stranded", e)
	}
	if e := rev4Exchange(p, 124, 32, 750*time.Millisecond); e != nil {
		t.Fatal("post-bulk light request", e)
	}
	f.mu.Lock()
	l := f.serverLanes["a01"]
	f.mu.Unlock()
	u, e := l.data.UsageByOrigin(l.account.ID, l.user.ID, l.cipher.Context().Session)
	if e != nil || u.HistoricalRemote != (pool.Amount{Bytes: 11329548, Messages: 166}) {
		t.Fatalf("old debt changed: %+v err=%v", u, e)
	}
	if e = l.data.Audit(); e != nil {
		t.Fatal(e)
	}
}
