package tunnel

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"
)

// Long DATA retry sleeps used to survive a successful control reattachment.
// Verify the account-wide gate and worker sleeps share the authenticated
// survival budget, while cold and prolonged outages keep exponential pacing.
func TestRev5AccountDATARecoverySharesAuthenticatedBudget(t *testing.T) {
	g := newAccountDialGate()
	if err := g.run(context.Background(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.recoveryUntil = time.Now().Add(-time.Hour) // a healthy account can be idle for hours before its first outage
	g.mu.Unlock()
	for range 10 {
		g.mu.Lock()
		g.next = time.Time{}
		g.mu.Unlock()
		if err := g.run(context.Background(), func() error { return syscall.ENETUNREACH }); !errors.Is(err, syscall.ENETUNREACH) {
			t.Fatal(err)
		}
		g.mu.Lock()
		delay := g.delay
		g.mu.Unlock()
		if delay > 2*time.Second || g.repairDelay(30*time.Second) > 2*time.Second {
			t.Fatal("authenticated DATA retained a cold retry sleep", delay)
		}
	}
	g.mu.Lock()
	g.recoveryUntil = time.Now().Add(-time.Second)
	g.next = time.Time{}
	g.delay = 16 * time.Second
	g.mu.Unlock()
	_ = g.run(context.Background(), func() error { return syscall.ENETUNREACH })
	if g.repairDelay(30*time.Second) != 30*time.Second {
		t.Fatal("prolonged outage lost pacing")
	}
	g.mu.Lock()
	delay := g.delay
	g.mu.Unlock()
	if delay != 30*time.Second {
		t.Fatal("account cooldown lost cold cap", delay)
	}
	var absent *accountDialGate
	if absent.repairDelay(time.Second) != time.Second {
		t.Fatal("standalone retry changed")
	}
}
