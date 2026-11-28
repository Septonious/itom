package mailstream

import (
	"context"
	"sort"
	"time"
)

// Keep every server control socket authenticated, but don't flood the
// provider with active-rate FETCH loops for the seven unallocated mailboxes.
// These can still carry a new HELLO or become part of a grant immediately.
func (a *mailAccountState) controlReadDelay() time.Duration {
	l := a.link
	if l.client {
		return time.Millisecond
	}
	l.mu.Lock()
	standby := l.grantSession != (SessionID{}) && !l.grant[a.config.ID]
	l.mu.Unlock()
	if standby {
		return 40 * time.Millisecond
	}
	return time.Millisecond
}

// Only the independent interactive APPEND completion updates this score.
// Use a short EWMA plus dispersion rather than selecting lucky minima.
func (a *mailAccountState) observeControlPublication(d time.Duration) {
	sample := int64(d)
	if sample <= 0 {
		return
	}
	for old := a.controlServiceMean.Load(); ; old = a.controlServiceMean.Load() {
		next := sample
		if old > 0 {
			next = (7*old + sample) / 8
		}
		if a.controlServiceMean.CompareAndSwap(old, next) {
			if old > 0 {
				delta := sample - old
				if delta < 0 {
					delta = -delta
				}
				for prev := a.controlServiceJitter.Load(); ; prev = a.controlServiceJitter.Load() {
					if a.controlServiceJitter.CompareAndSwap(prev, (7*prev+delta)/8) {
						break
					}
				}
			}
			break
		}
	}
	a.controlServiceAt.Store(time.Now().UnixNano())
}

func (l *IMAPLink) idleInteractiveOrder(start int, allowed [20]bool, excluded *mailAccountState) []int {
	now := time.Now()
	bulk := l.lastBulk.Load()
	if bulk != 0 && now.Sub(time.Unix(0, bulk)) < time.Second {
		return nil
	}
	// Keep one full-pool round-robin exploration position for stale estimates.
	if start == 0 {
		return nil
	}
	candidates := make([]interactiveRoute, 0, len(l.accounts))
	measured := 0
	for n := range l.accounts {
		i := (start + n) % len(l.accounts)
		a := l.accounts[i]
		cost := 24 * time.Hour
		if allowed[i] && a != excluded && a.mu.TryLock() {
			if a.ready && !a.failed && !a.writerRecovering && a.controlWriter != nil && !a.parking.Load() {
				cost = 80 * time.Millisecond
				at := a.controlServiceAt.Load()
				mean := a.controlServiceMean.Load()
				if at > 0 && mean > 0 && now.Sub(time.Unix(0, at)) <= 15*time.Second {
					cost = time.Duration(mean + a.controlServiceJitter.Load())
					measured++
				}
				// Queued literals and occupied tagged-result slots are real service debt.
				debt := len(a.interactive) + len(a.controlWriter.interactiveTags)
				if !l.mobileFeedback.Load() {
					debt += int(a.controlWriter.ordinaryControlTags.Load()) + (len(a.fast)+15)/16
				}
				cost += time.Duration(debt) * max(20*time.Millisecond, cost/2)
			}
			a.mu.Unlock()
		}
		candidates = append(candidates, interactiveRoute{i, cost})
	}
	if measured < 2 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].cost/(5*time.Millisecond) < candidates[j].cost/(5*time.Millisecond)
	})
	order := make([]int, len(candidates))
	for i, c := range candidates {
		order[i] = c.index
	}
	return order
}

// One bounded reconnect at the last fully consumed control UID. A changed
// UIDVALIDITY or failed attempt is never accepted; grant repair can use a spare.
func (a *mailAccountState) recoverControlReader(old *mailPipe) (*mailPipe, error) {
	if old == nil || old.validity == 0 || old.invalid.Load() {
		return nil, ErrProtocol
	}
	a.phase("control_reconnecting")
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Second)
	defer cancel()
	select {
	case a.link.login <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-a.link.login }()
	fresh, err := a.reopenReader(ctx, &readerCheckpoint{pipe: old, folder: a.link.controlFolder(false)}, false, SessionID{})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.control = fresh
	a.ready = true
	a.stage = "serving"
	a.failure = ""
	a.mu.Unlock()
	a.controlReconnects.Add(1)
	a.link.notify()
	return fresh, nil
}

// Called with a.mu held and a verified companion. A partially occupied
// publisher is a fallback, not an idle path. Both actors publish to the same
// physical socket, so count ordinary control tags and queued feedback too.
func (a *mailAccountState) idleInteractivePublisher(mobile bool) bool {
	p := a.controlWriter
	if len(a.interactive) != 0 {
		return false
	}
	if mobile {
		return len(p.interactiveTags) < cap(p.interactiveTags)
	}
	return len(p.interactiveTags) == 0 && p.ordinaryControlTags.Load() == 0 && len(a.fast) == 0
}
