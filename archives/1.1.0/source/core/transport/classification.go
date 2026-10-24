package transport

import "time"

type interactiveBudget struct {
	tokens  float64
	updated time.Time
}

func (b *interactiveBudget) refill(now time.Time, rate uint64, capacity int) {
	if b.updated.IsZero() {
		b.tokens = float64(capacity)
		b.updated = now
		return
	}
	if !now.After(b.updated) {
		return
	}
	elapsed := now.Sub(b.updated).Seconds()
	b.updated = now
	b.tokens += float64(rate) * elapsed
	if b.tokens > float64(capacity) {
		b.tokens = float64(capacity)
	}
}
func (s *Session) shortLocked(f *stream) bool {
	n := f.tx.Len()
	return n > 0 && n <= s.limits.InteractiveBytes && float64(n) <= f.interactive.tokens && float64(n) <= s.interactive.tokens
}

// A continuing light flow must replenish without a mandatory silence period.
// Both per-flow and aggregate budgets bound use of the fast DATA path. Bulk
// spending never steals another flow's global interactive allowance.
func (s *Session) refillInteractiveLocked(now time.Time) {
	oldGlobal := s.interactive.tokens
	s.interactive.refill(now, s.limits.LightBytesPerSecond, s.limits.InteractiveBytes)
	becameReady := false
	for _, f := range s.flows {
		if f.stopped() {
			continue
		}
		n := f.tx.Len()
		was := n > 0 && n <= s.limits.InteractiveBytes && float64(n) <= oldGlobal && float64(n) <= f.interactive.tokens
		f.interactive.refill(now, s.limits.LightBytesPerSecond, s.limits.InteractiveBytes)
		if !was && s.shortLocked(f) {
			becameReady = true
		}
	}
	if becameReady {
		s.signalOutgoing()
	}
}
