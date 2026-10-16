package tunnel

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A real authenticated TLS base socket must remain useful when every extra
// LOGIN stalls. Rev5 waited its 30s dial timeout before sending CONFIRM.
func TestRev6StalledOptionalLoginsKeepAuthenticatedBaseUsable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cm, sm := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
	m := cm.(*laneMail)
	m.stageSecondDataWriter = true
	var stalled atomic.Int64
	m.dial = func(call context.Context) (*mailbox.Client, error) {
		stalled.Add(1)
		<-call.Done()
		return nil, call.Err()
	}
	start := time.Now()
	if err := m.Activate(ctx, "itom-down"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatal("optional LOGIN blocked activation", d)
	}
	if ctx.Err() != nil || !m.DataAvailable() || stalled.Load() == 0 {
		t.Fatal("base fallback unavailable", ctx.Err(), stalled.Load())
	}
	raw, err := mailbox.Encode([]byte("CONFIRM must not wait for optional LOGIN"))
	if err != nil {
		t.Fatal(err)
	}
	if err = m.AppendControl(ctx, "itom-up", raw); err != nil {
		t.Fatal("base control failed", err)
	}
	rows, _, err := sm.Page(ctx, "itom-up", mailbox.Cursor{})
	if err != nil || len(rows) != 1 {
		t.Fatal("control not delivered", err, len(rows))
	}
	m.Close()
}
func TestRev6OptionalLoginBudgetNeverSwallowsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	m.dial = func(call context.Context) (*mailbox.Client, error) { <-call.Done(); return nil, call.Err() }
	if err := m.Activate(ctx, "down"); err == nil {
		t.Fatal("parent deadline swallowed")
	}
}

// Match the field startup: half the logins fail; the others authenticate, but
// every extra client socket stalls. Real supervisors, TLS IMAP, Noise, grants
// and a tunneled TCP echo must become usable before the old 30-second wait.
func TestRev6HealthyAccountsActivateWhileOtherLoginsFailAndExtrasStall(t *testing.T) {
	var allow atomic.Bool
	var rejected, stalled atomic.Int64
	var mu sync.Mutex
	counts := map[string]int{}
	started := time.Now()
	policy := func(next Dial) Dial {
		return func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
			if cfg.Username == "a01" || cfg.Username == "a03" || cfg.Username == "a05" {
				rejected.Add(1)
				return nil, errors.New("IMAP_AUTH_OR_GREETING_FAILED")
			}
			mu.Lock()
			counts[cfg.Username]++
			nth := counts[cfg.Username]
			mu.Unlock()
			if nth > 1 && !allow.Load() {
				stalled.Add(1)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(30 * time.Second):
				}
			}
			return next(ctx, cfg, folders)
		}
	}
	f := newProductionSupervisorFixtureState(t, 6, func(_, _ string, _ servercfg.Config, _ servercfg.User) {}, policy)
	for _, id := range []string{"a02", "a04", "a06"} {
		f.waitLane(t, id, 0)
	}
	if elapsed := time.Since(started); elapsed > 12*time.Second {
		t.Fatal("healthy accounts blocked by failed/extra logins", elapsed)
	}
	if rejected.Load() == 0 || stalled.Load() == 0 {
		t.Fatal("missing field failure injection")
	}
	c := rev4Open(t, f)
	defer c.Close()
	if err := rev4Exchange(c, 600, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("activated connection unusable", err)
	}
	allow.Store(true)
	t.Logf("healthy_startup_ms=%d rejected=%d optional_stalled=%d", time.Since(started).Milliseconds(), rejected.Load(), stalled.Load())
}

// More than the two idle periods seen in the capture, with no application
// traffic during the wait. The provider expires sockets with no commands after
// 45 seconds. A later burst must use the same carrier epochs, not a new login.
func TestRev6IdleAccountsKeepSocketsAndNextTCPBurstUsable(t *testing.T) {
	t.Setenv("ITOM_LOAD_PROVIDER_IDLE_TIMEOUT_MS", "45000")
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	f := newProductionSupervisorFixtureState(t, 15, rev4RetainedState(t))
	epochs := map[string]uint64{}
	for i := 1; i <= 15; i++ {
		id := fmt.Sprintf("a%02d", i)
		epochs[id] = f.waitLane(t, id, 0).carrierEpoch
	}
	if err := pause(f.ctx, 110*time.Second); err != nil {
		t.Fatal(err)
	}
	for id, epoch := range epochs {
		l := f.waitLane(t, id, 0)
		if l.carrierEpoch != epoch {
			t.Fatal("idle carrier restarted", id, epoch, l.carrierEpoch)
		}
	}
	if expired := f.network.idleExpired.Load(); expired != 0 {
		t.Fatal("idle sockets expired and required LOGIN", expired)
	}
	c := rev4Open(t, f)
	defer c.Close()
	if err := rev4Exchange(c, 601, 8192, 1100*time.Millisecond); err != nil {
		t.Fatal("idle recovery burst failed", err)
	}
	t.Log("idle_seconds=110 accounts=15 provider_idle_expiry_seconds=45 same_carrier_epochs=true")
}
