package mailstream

import "time"

// Preserve the exact 1 ms poll gap of the field-tested RC1 in both active
// and quiet states. An idle 3/8/16 ms backoff was rejected for RC2: the
// first incoming packet after quiet could otherwise incur up to 15 ms of
// added latency. Reduce command overhead only through an independently
// qualified event-driven path with no added first-packet RTT.
func controlPollInterval(_ time.Duration) time.Duration { return time.Millisecond }

// Keep the diagnostic and call sites intact without introducing new
// scheduling, timers, or activity-dependent decisions on the receive path.
func (p *mailPipe) controlPollDelay(_ time.Time) time.Duration {
	return time.Millisecond
}
