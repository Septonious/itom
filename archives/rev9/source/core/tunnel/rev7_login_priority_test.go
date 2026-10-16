package tunnel

import (
	"context"
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"sync/atomic"
	"testing"
	"time"
)

func TestRev7ControlAndReceiveRepairBypassQueuedOptionalLogins(t *testing.T) {
	var fault atomic.Bool
	var slow atomic.Int64
	policy := func(next Dial) Dial {
		return func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
			if fault.Load() && !priorityLogin(ctx) {
				slow.Add(1)
				if err := pause(ctx, 2*time.Second); err != nil {
					return nil, err
				}
			}
			return next(ctx, cfg, folders)
		}
	}
	f := newProductionSupervisorFixtureState(t, 15, rev4RetainedState(t), policy)
	var lanes []*lane
	for i := 1; i <= 15; i++ {
		lanes = append(lanes, f.waitLane(t, fmt.Sprintf("a%02d", i), 0))
	}
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 800, 4096, 1100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	fault.Store(true)
	for _, l := range lanes {
		m := physicalMail(l.mail)
		if r := m.readerRef(); r != nil {
			r.Close()
		}
		if r := m.dataReaderRef(); r != nil {
			r.Close()
		}
		m.cleanerMu.Lock()
		if m.cleaner != nil {
			m.cleaner.Close()
		}
		m.cleanerMu.Unlock()
		m.writerMu.Lock()
		for _, c := range m.writers {
			if c != nil {
				c.Close()
			}
		}
		m.writerMu.Unlock()
	}
	deadline := time.Now().Add(4 * time.Second)
	for slow.Load() < 4 {
		if time.Now().After(deadline) {
			t.Fatal("optional LOGIN pressure missing", slow.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, l := range lanes {
		physicalMail(l.mail).Client.Close()
		if err := l.controlSend(wire.Echo{Token: 800}); err != nil {
			t.Fatal(err)
		}
	}
	deadline = time.Now().Add(12 * time.Second)
	for {
		ready := 0
		for _, l := range lanes {
			if l.ctx.Err() != nil {
				t.Fatal("critical repair starved behind optional LOGINs", l.account.ID, l.carrierEpoch)
			}
			m := physicalMail(l.mail)
			if m.controlClient() != m.Client && m.controlClient().Ready() {
				ready++
			}
		}
		if ready == len(lanes) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control repairs missed survival budget", ready)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := rev4Exchange(chat, 801, 4096, 5*time.Second); err != nil {
		t.Fatal("original chat after all socket EOFs", err)
	}
	fault.Store(false)
	for _, l := range lanes {
		if l.ctx.Err() != nil {
			t.Fatal("carrier lost", l.account.ID)
		}
	}
	t.Logf("accounts=15 optional_logins_slow=%d critical_repairs=15 unchanged_carrier_epochs=true original_chat=true", slow.Load())
}

func TestRev7LoginPriorityCancellationDoesNotLeakCapacity(t *testing.T) {
	g := &loginAdmission{limit: 1}
	release, err := g.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := g.acquire(ctx, true); done <- e }()
	cancel()
	if e := <-done; e != context.Canceled {
		t.Fatal(e)
	}
	release()
	release()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active != 0 || len(g.urgent) != 0 || len(g.ordinary) != 0 {
		t.Fatal("canceled LOGIN retained reservation")
	}
}
