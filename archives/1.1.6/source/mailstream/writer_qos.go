package mailstream

import "time"

// The priority and bulk APPEND sockets share one actor and a completion
// budget of four. A short interactive burst borrows the fourth slot while
// DATA can still use three slots. No penalty at bulk-only steady state.
// This is a scheduling hint, not a new queue or publication path.
func (l *IMAPLink) reserveInteractiveWriterSlot(inFlight int, now time.Time) bool {
	if inFlight != 3 {
		return false
	}
	at := l.lastInteractive.Load()
	if at <= 0 {
		return false
	}
	age := now.Sub(time.Unix(0, at))
	return age >= 0 && age <= 180*time.Millisecond
}
