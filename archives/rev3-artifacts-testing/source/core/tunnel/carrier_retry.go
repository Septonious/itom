package tunnel

import (
	"math/rand/v2"
	"time"
)

// A working carrier gets one immediate recovery attempt. Repeated connect
// failures retain exponential pacing; a flapping authenticated endpoint cannot
// replenish that immediate attempt more often than once every ten seconds.
// Each supervisor owns its policy, so one account cannot delay the other ones.
type carrierRetryPolicy struct {
	backoff       time.Duration
	lastImmediate time.Time
}

func (p *carrierRetryPolicy) next(now time.Time, wasAuthenticated bool) time.Duration {
	if wasAuthenticated && (p.lastImmediate.IsZero() || now.Sub(p.lastImmediate) >= 10*time.Second) {
		p.lastImmediate = now
		p.backoff = time.Second
		return time.Duration(rand.Int64N(int64(400 * time.Millisecond)))
	}
	if p.backoff < time.Second {
		p.backoff = time.Second
	}
	delay := p.backoff + time.Duration(rand.Int64N(int64(p.backoff/2)+1))
	p.backoff = min(16*time.Second, 2*p.backoff)
	return delay
}
