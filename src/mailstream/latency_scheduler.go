package mailstream

import (
	"time"
)

// An empty UID FETCH gives a fresh provider service measurement without
// interrupting the control reader with another IMAP command.
func (p *mailPipe) observeEmptyControlFetch(started time.Time) {
	now := time.Now()
	sample := int64(now.Sub(started))
	if sample <= 0 {
		return
	}
	for old := p.latency.Load(); ; old = p.latency.Load() {
		next := sample
		if old > 0 {
			next = (7*old + sample) / 8
		}
		if p.latency.CompareAndSwap(old, next) {
			break
		}
	}
	p.latencyAt.Store(now.UnixNano())
}

type interactiveRoute struct {
	index int
	cost  time.Duration
}
