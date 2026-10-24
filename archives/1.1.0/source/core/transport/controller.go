package transport

import "time"

type Plane uint8

const (
	Fast Plane = iota
	Bulk
)

// ServiceModel consumes receiver-confirmed useful delivery, never APPEND bytes
// or maintenance echoes. No RTT-based throughput backoff is applied. Memory,
// receiver credit and exact physical permission remain independent limits.
type ServiceModel struct {
	rate        float64
	propagation time.Duration
	last        time.Time
	minimumAt   time.Time
	samples     uint64
}

func (m *ServiceModel) Observe(now time.Time, useful uint64, interval, feedback time.Duration, applicationLimited bool) {
	if interval <= 0 || feedback <= 0 || useful == 0 || (!m.last.IsZero() && !now.After(m.last)) {
		return
	}
	m.last = now
	if m.propagation == 0 || feedback < m.propagation || (applicationLimited && now.Sub(m.minimumAt) >= 10*time.Second) {
		m.propagation = feedback
		m.minimumAt = now
	}
	if applicationLimited {
		return
	}
	rate := float64(useful) / interval.Seconds()
	// Fast acquisition, slow aging, so an app-limited gap cannot erase capacity.
	if m.samples == 0 || rate > m.rate {
		m.rate = rate
	} else {
		m.rate = m.rate*.9 + rate*.1
	}
	m.samples++
}
func (m *ServiceModel) Window(initial, ceiling uint64) uint64 {
	if initial > ceiling {
		return ceiling
	}
	if m.samples == 0 {
		return initial
	}
	// Size from propagation, not loaded queueing RTT. Keep sufficient flight for
	// feedback batching without requiring a long, additive slow start.
	n := m.rate * m.propagation.Seconds() * 2
	if n >= float64(ceiling) {
		return ceiling
	}
	if n < float64(initial) {
		return initial
	}
	return uint64(n)
}
func (m *ServiceModel) Batch(ready, permission int, maxService time.Duration) int {
	if ready <= 0 || permission <= 0 {
		return 0
	}
	n := ready
	if n > permission {
		n = permission
	}
	if n > MaxPayload {
		n = MaxPayload
	}
	if m.rate > 0 && maxService > 0 {
		turnFloat := m.rate * maxService.Seconds()
		if turnFloat >= float64(n) {
			return n
		}
		turn := int(turnFloat)
		if turn < 1 {
			turn = 1
		}
		if n > turn {
			n = turn
		}
	}
	return n // No fill timer: immediately publish available admissible work.
}
