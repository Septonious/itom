package mailstream

import (
	"sort"
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
	cost time.Duration
}

// Only small interactive data/control uses the ranked candidate order;
// bulk/feedback retain the unchanged 1.1.5 placement. The admission path
// independently validates every account after ranking.
func (l *IMAPLink) interactiveAccountOrder(start int, allowed [20]bool, excluded *mailAccountState, interactive bool) []int {
	candidates := make([]interactiveRoute, len(l.accounts))
	now := time.Now()
	for n := range candidates {
		index := (start + n) % len(l.accounts)
		candidates[n] = interactiveRoute{index: index}
		if !interactive {
			continue
		}
		a := l.accounts[index]
		if !allowed[index] || a == excluded || !a.mu.TryLock() {
			candidates[n].cost = 24 * time.Hour
			continue
		}
		cost := 24 * time.Hour
		if a.ready && !a.failed && !a.writerRecovering && !a.reclaiming.Load() {
			readService := time.Duration(0)
			if p := a.control; p != nil {
				at := p.latencyAt.Load()
				if at > 0 && now.Sub(time.Unix(0, at)) <= 30*time.Second {
					readService = time.Duration(p.latency.Load())
				}
			}
			appendService := time.Duration(a.appendLatency.Load())
			if readService <= 0 {
				readService = appendService
			}
			if appendService <= 0 {
				appendService = readService
			}
			if readService <= 0 {
				readService, appendService = 50*time.Millisecond, 50*time.Millisecond
			}
			service := max(readService, appendService)
			cost = readService + appendService + time.Duration(len(a.fast))*service
			// Control and DATA APPEND sockets share an actor/completion budget.
			if len(a.bulk) >= 8 {
				cost += service
			}
		}
		a.mu.Unlock()
		// Avoid oscillation from insignificant command-time jitter.
		candidates[n].cost = (cost / (5 * time.Millisecond)) * (5 * time.Millisecond)
	}
	if interactive {
		sort.SliceStable(candidates, func(i, j int) bool {
			return candidates[i].cost < candidates[j].cost
		})
	}
	order := make([]int, len(candidates))
	for i, entry := range candidates {
		order[i] = entry.index
	}
	return order
}
