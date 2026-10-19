package tunnel

import (
	"math/rand/v2"
	"time"
)

// A working carrier gets one immediate recovery attempt. For its existing
// logical-session survival interval, bounded retry keeps a transient outage
// from adding another 16-24 seconds after the provider has already recovered.
// Cold endpoints and prolonged failures retain exponential pacing; a flapping
// authenticated endpoint cannot replenish immediate retry more than once/10s.
// Each supervisor owns its policy, so one account cannot delay the other ones.
type carrierRetryPolicy struct {
	backoff       time.Duration
	lastImmediate time.Time
	recoveryUntil time.Time
}

func (p *carrierRetryPolicy) next(now time.Time, wasAuthenticated bool) time.Duration {
	if wasAuthenticated {
		p.recoveryUntil = now.Add(logicalProgressLimit)
	}
	if wasAuthenticated && (p.lastImmediate.IsZero() || now.Sub(p.lastImmediate) >= 10*time.Second) {
		p.lastImmediate = now
		p.backoff = time.Second
		return time.Duration(rand.Int64N(int64(400 * time.Millisecond)))
	}
	if p.backoff < time.Second {
		p.backoff = time.Second
	}
	ceiling := 16 * time.Second
	if now.Before(p.recoveryUntil) {
		ceiling = 2 * time.Second
	}
	p.backoff = min(p.backoff, ceiling)
	delay := p.backoff + time.Duration(rand.Int64N(int64(p.backoff/2)+1))
	p.backoff = min(ceiling, 2*p.backoff)
	return delay
}
