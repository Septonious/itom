//go:build itomtest

package tunnel

import (
	"itom.local/core/pool"
	"os"
	"testing"
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
