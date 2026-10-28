package transport

import "time"

// Historical launches yield during the original short-request lifetime. This
// changes scheduling, never a request deadline, permission or recovery proof.
// Current grants and replies to already admitted recovery are always serviced.
// Under bulk pressure recovery keeps running so capacity cannot be stranded.
func (s *RosterService) historicalLaunchAllowed() bool {
	if s.Runtime == nil {
		return true
	} // policy component construction
	return s.Runtime.session.historicalLaunchAllowed(time.Now())
}
func (s *Session) historicalLaunchAllowed(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastForeground.IsZero() || now.Sub(s.lastForeground) >= openTimeout || s.buffered > uint64(s.limits.InteractiveBytes) || s.received > uint64(s.limits.InteractiveBytes)
}
