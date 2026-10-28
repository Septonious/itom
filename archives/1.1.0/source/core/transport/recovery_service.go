package transport

import (
	"errors"
	"itom.local/core/journal"
	"time"
)

// Historical launches yield during the original short-request lifetime. This
// changes scheduling, never a request deadline, permission or recovery proof.
// Current grants and replies to already admitted recovery are always serviced.
// Under bulk pressure recovery keeps running so capacity cannot be stranded.
func (s *RosterService) historicalLaunchAllowed() bool {
	if s.Runtime == nil {
		return true
	} // policy component construction
	return s.Runtime.session.historicalLaunchAllowed(time.Now()) || s.Runtime.permissionPressure()
}
func (s *Session) historicalLaunchAllowed(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastForeground.IsZero() || now.Sub(s.lastForeground) >= openTimeout || s.buffered > uint64(s.limits.InteractiveBytes) || s.received > uint64(s.limits.InteractiveBytes)
}

// Actual failed quota admission overrides foreground grace. Older surviving
// wallet grants may own the capacity needed by the current short request.
func (r *Runtime) permissionPressure() bool {
	for _, c := range r.carriers {
		stats := c.Writer.Stats()
		if stats.DataBlocked || stats.ControlBlocked {
			return true
		}
	}
	r.session.mu.Lock()
	owners := r.session.authorities
	r.session.mu.Unlock()
	for _, owner := range owners {
		owner.mu.Lock()
		blocked := false
		for _, issue := range owner.issues {
			blocked = blocked || errors.Is(issue.err, journal.ErrLimit) || errors.Is(issue.err, journal.ErrRecovery)
		}
		owner.mu.Unlock()
		if blocked {
			return true
		}
	}
	return false
}
