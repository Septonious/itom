package datapath

import "time"

// Per-socket observation, owned by readSocket. Preserve bulk aggregation and
// shrink only the first tiny read after a quiet interval. A burst immediately
// returns to normal aggregation; size alone never permanently classifies a flow.
// This changes accumulation time, not stream credit or physical authority.
type socketReadPolicy struct{ previous time.Time }

func (p *socketReadPolicy) budget(bytes int, now time.Time, normal, quiet time.Duration) time.Duration {
	wasQuiet := p.previous.IsZero() || now.Sub(p.previous) >= max(20*time.Millisecond, 4*normal)
	p.previous = now
	if quiet > 0 && bytes <= dataTinyThreshold && wasQuiet {
		return min(quiet, normal)
	}
	return normal
}
