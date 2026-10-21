package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"

	"itom.local/core/mailbox"
)

const accountByteQuantum = 256 << 10

// accountDialGate is shared by DATA repair, reader and maintenance on an
// account. A cooldown belongs to the account, not to an individual socket.
// Network IO runs outside mu; only one dial can pass admission at a time.
type accountDialGate struct {
	work          accountWorkBudget
	slot          *loginAdmission
	mu            sync.Mutex
	next          time.Time
	delay         time.Duration
	failures      uint64
	recoveryUntil time.Time
	authenticated bool
}

func newAccountDialGate() *accountDialGate { return &accountDialGate{slot: &loginAdmission{limit: 1}} }

func (g *accountDialGate) run(ctx context.Context, dial func() error) error {
	release, err := g.slot.acquire(ctx, priorityLogin(ctx))
	if err != nil {
		return err
	}
	defer release()
	g.mu.Lock()
	remaining := time.Until(g.next)
	generation := g.failures
	g.mu.Unlock()
	if remaining > 0 {
		if err := pause(ctx, remaining); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err = dial()
	g.mu.Lock()
	defer g.mu.Unlock()
	if err == nil {
		g.recoveryUntil = time.Now().Add(logicalProgressLimit)
		g.authenticated = true
		if generation == g.failures {
			g.delay = 0
			g.next = time.Time{}
		}
		return nil
	}
	// Configuration/authentication and intentional cancellation do not create
	// an account-wide retry storm or penalize other authenticated users.
	reason := mailbox.FailureReason(err)
	if ctx.Err() == nil && !errors.Is(err, errLocalConnectionLimit) && (connectionLimitFallback(err) || transientIMAPDialReason(reason)) {
		if g.authenticated {
			g.recoveryUntil = time.Now().Add(logicalProgressLimit)
			g.authenticated = false
		}
		if g.delay == 0 {
			g.delay = time.Second
		} else {
			g.delay = min(30*time.Second, 2*g.delay)
		}
		g.failures++
		g.delay = g.repairDelayLocked(g.delay)
		g.next = time.Now().Add(g.delay)
	}
	return err
}

// transientIMAPDialReason is intentionally a whitelist: retry a lost network,
// never a bad credential, certificate, permission or permanent DNS failure.
// The same decision is used by the account dial gate and DATA worker, so an
// account outage cannot spin on repeated connections or prematurely discard
// already-admitted (but not yet APPENDed) DATA.
func transientIMAPDialReason(reason string) bool {
	switch reason {
	case "eof", "reset", "timeout", "temporary", "unreachable", "refused", "socket_closed", "bye":
		return true
	default:
		return false
	}
}

// Known working accounts retain their existing logical-session recovery budget.
// DATA workers and the shared dial gate must agree on this ceiling: independent
// exponential sleeps otherwise keep DATA unavailable after control reattaches.
// Cold accounts and outages beyond the survival interval retain the 30s cap.
func (g *accountDialGate) repairDelay(delay time.Duration) time.Duration {
	if g == nil {
		return delay
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.repairDelayLocked(delay)
}
func (g *accountDialGate) repairDelayLocked(delay time.Duration) time.Duration {
	if time.Now().Before(g.recoveryUntil) {
		return min(delay, 2*time.Second)
	}
	return delay
}
