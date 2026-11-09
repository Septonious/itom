package mailstream

import (
	"slices"
	"time"
)

// interactiveHedgeAccountOrder only alters placement of the ALREADY existing
// second copy of an isolated <=4 KiB interactive packet. The primary packet,
// bulk stripes, WINDOW and ordinary feedback remain round-robin.
//
// Rely on authenticated per-account packet/receipt RTT, not IMAP FETCH/APPEND
// service time: the field capture showed 6–8 ms IMAP commands but ~180 ms
// end-to-end delivery and intermittent 300+ ms account tails.
//
// Avoid ranking an untrained/new account as fast. When at least four fresh
// samples exist, rotate among the faster two-thirds of accounts rather than
// pinning one allegedly fastest provider login. All others remain in order
// as normal fallback if their better peers are busy or near their budget.
func (l *IMAPLink) interactiveHedgeAccountOrder(start int, allowed [20]bool, exclude *mailAccountState) []int {
	n := len(l.accounts)
	order := make([]int, 0, n)
	if n == 0 {
		return order
	}
	for step := 0; step < n; step++ {
		order = append(order, (start+step)%n)
	}
	if n < 4 {
		return order
	}
	now := time.Duration(time.Now().UnixNano())
	cost := make([]time.Duration, n)
	known := make([]time.Duration, 0, n)
	for _, index := range order {
		if !allowed[index] {
			continue
		}
		a := l.accounts[index]
		if a == exclude || !a.mu.TryLock() {
			continue
		}
		if a.ready && !a.failed && !a.writerRecovering && !a.reclaiming.Load() &&
			a.rtt > 0 && a.rttAt > 0 && now-a.rttAt >= 0 && now-a.rttAt < 30*time.Second {
			c := a.rtt
			if len(a.fast) > 1 {
				c += time.Duration(len(a.fast)-1) * 10 * time.Millisecond
			}
			cost[index] = c
			known = append(known, c)
		}
		a.mu.Unlock()
	}
	if len(known) < 4 {
		return order
	}
	slices.Sort(known)
	cutoff := known[(len(known)*2)/3]
	fast := make([]int, 0, n)
	remaining := make([]int, 0, n)
	for _, index := range order {
		if cost[index] > 0 && cost[index] <= cutoff {
			fast = append(fast, index)
		} else {
			remaining = append(remaining, index)
		}
	}
	return append(fast, remaining...)
}
