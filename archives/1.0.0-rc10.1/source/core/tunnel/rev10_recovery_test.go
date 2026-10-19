package tunnel

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Pre-test discovery, a quiet interval, and every client physical role lost.
// A transient first control replacement must not destroy the logical carrier.
func TestRev10DiscoveryThenAllClientSocketsLost(t *testing.T) {
	var fault atomic.Bool
	var mu sync.Mutex
	failed := map[string]bool{}
	policy := func(next Dial) Dial {
		return func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
			control, _ := ctx.Value(controlAdmissionKey{}).(bool)
			if fault.Load() && control {
				mu.Lock()
				first := !failed[cfg.Username]
				failed[cfg.Username] = true
				mu.Unlock()
				if first {
					return nil, io.EOF
				}
			}
			return next(ctx, cfg, folders)
		}
	}
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	f := newProductionSupervisorFixtureState(t, 10, rev4RetainedState(t), policy)
	for i := 1; i <= 10; i++ {
		f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
	}
	original := rev4Open(t, f)
	if err := rev4Exchange(original, 1000, 4096, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		errs := make(chan error, 12)
		for i := 0; i < 12; i++ {
			c := rev4Open(t, f)
			go func(label int) { defer c.Close(); errs <- rev4Exchange(c, label, 4096, 3*time.Second) }(1010 + round*12 + i)
		}
		for i := 0; i < 12; i++ {
			if err := <-errs; err != nil {
				t.Fatal("discovery", err)
			}
		}
	}
	time.Sleep(1100 * time.Millisecond)
	var lanes []*lane
	for i := 1; i <= 10; i++ {
		lanes = append(lanes, f.waitLane(t, fmt.Sprintf("a%02d", i), 0))
	}
	fault.Store(true)
	closed := 0
	for _, l := range lanes {
		m := physicalMail(l.mail)
		if c := m.readerRef(); c != nil {
			c.Close()
			closed++
		}
		if c := m.dataReaderRef(); c != nil {
			c.Close()
			closed++
		}
		m.cleanerMu.Lock()
		if m.cleaner != nil {
			m.cleaner.Close()
			closed++
		}
		m.cleanerMu.Unlock()
		m.writerMu.Lock()
		for _, c := range m.writers {
			if c != nil {
				c.Close()
				closed++
			}
		}
		m.writerMu.Unlock()
		m.controlClient().Close()
		closed++
	}
	started := time.Now()
	for _, l := range lanes {
		if err := l.controlSend(wire.Echo{Token: 10001}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		ready := 0
		for _, l := range lanes {
			if l.ctx.Err() != nil {
				t.Fatalf("transient repair destroyed original carrier account=%s", l.account.ID)
			}
			m := physicalMail(l.mail)
			if m.controlClient() != m.Client && m.controlClient().Ready() && m.DataAvailable() && m.readerRef().Ready() && (l.dataShards == 0 || m.dataReaderRef().Ready()) {
				ready++
			}
		}
		if ready == len(lanes) && f.client.Snapshot().ActiveLines == 20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d minimum-service paths recovered", ready, len(lanes))
		}
		time.Sleep(10 * time.Millisecond)
	}
	recovered := time.Since(started)
	if err := rev4Exchange(original, 1080, 4096, 3*time.Second); err != nil {
		t.Fatal("original TCP after correlated EOF", err)
	}
	fresh := rev4Open(t, f)
	defer fresh.Close()
	if err := rev4Exchange(fresh, 1081, 256<<10, 6*time.Second); err != nil {
		t.Fatal("fresh transfer after repair", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := rev4Exchange(original, 1082, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal("original TCP after recovery quiet", err)
	}
	for _, l := range lanes {
		if l.ctx.Err() != nil {
			t.Fatal("carrier restarted", l.account.ID)
		}
		if err := l.control.Audit(); err != nil {
			t.Fatal(err)
		}
		if err := l.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	n := len(failed)
	mu.Unlock()
	if n != 10 {
		t.Fatalf("faults reached only %d control repairs", n)
	}
	t.Logf("accounts=10 discovery_connections=24 client_socket_closes=%d injected_control_login_eofs=%d minimum_service_recovery_ms=%d active_lines_restored=20 original_tcp_retained=true fresh_transfer_verified=true carrier_restarts=0", closed, n, recovered.Milliseconds())
}
