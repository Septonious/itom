package mailstream

import (
	"sort"
	"time"
)

// Publication cost is deliberately not called delivery RTT: the tagged APPEND
// result can precede receiver visibility, FETCH, reassembly and its receipt.
// The carrier also measures authenticated first-attempt receipts. That round
// trip includes reverse feedback; only relative excess over other fresh paths
// is added to the local publication cost, and never interpreted as one-way RTT.
type bulkService struct {
	mean, jitter   time.Duration
	bytesPerSecond float64
	at             time.Time
}

func (m *bulkService) observe(n int, d time.Duration, now time.Time) {
	if n <= 0 || d <= 0 {
		return
	}
	rate := float64(n) / d.Seconds()
	if m.mean == 0 || now.Sub(m.at) > 15*time.Second {
		*m = bulkService{mean: d, bytesPerSecond: rate, at: now}
		return
	}
	delta := d - m.mean
	if delta < 0 {
		delta = -delta
	}
	m.jitter = (7*m.jitter + delta) / 8
	m.mean = (7*m.mean + d) / 8
	m.bytesPerSecond = (7*m.bytesPerSecond + rate) / 8
	m.at = now
}

func (m bulkService) fresh(now time.Time) bool {
	return !m.at.IsZero() && !now.Before(m.at) && now.Sub(m.at) <= 15*time.Second && m.bytesPerSecond > 0 && m.mean > 0
}

func (m bulkService) cost(queued, inFlight, next int, now time.Time) time.Duration {
	if !m.fresh(now) {
		return 24 * time.Hour
	}
	// Avoid estimating huge time durations from a nearly stalled service.
	seconds := float64(max(0, queued)+max(0, inFlight)+max(0, next)) / max(1, m.bytesPerSecond)
	return m.mean + 2*m.jitter + time.Duration(min(seconds, 60)*float64(time.Second))
}

type receiptService struct {
	mean, jitter time.Duration
	at           time.Time
	samples      uint64
}

func (m *receiptService) observe(d time.Duration, now time.Time) {
	if d <= 0 {
		return
	}
	if m.samples == 0 || now.Sub(m.at) > 15*time.Second || now.Before(m.at) {
		*m = receiptService{mean: d, at: now, samples: 1}
		return
	}
	delta := d - m.mean
	if delta < 0 {
		delta = -delta
	}
	m.mean = (7*m.mean + d) / 8
	m.jitter = (7*m.jitter + delta) / 8
	m.at = now
	m.samples++
}
func (m receiptService) fresh(now time.Time) bool {
	return m.samples >= 4 && !now.Before(m.at) && now.Sub(m.at) <= 15*time.Second
}

func (l *IMAPLink) bulkPublicationOrder(start int, allowed [20]bool, excluded *mailAccountState, charge int, now time.Time) []int {
	candidates := make([]interactiveRoute, 0, len(l.accounts))
	measured := 0
	var receiptCosts [20]time.Duration
	var bestReceipt time.Duration
	receiptMeasured := 0
	for n := range l.accounts {
		i := (start + n) % len(l.accounts)
		a := l.accounts[i]
		cost := 24 * time.Hour
		if allowed[i] && a != excluded && a.mu.TryLock() {
			if a.ready && !a.failed && !a.writerRecovering && a.writer != nil && !a.bulkBlocked && !a.parking.Load() && !a.scaleBlocked.Load() && !a.reclaiming.Load() && a.bulkService.fresh(now) {
				cost = a.bulkService.cost(a.queuedBytes, int(a.bulkInFlight.Load()), charge, now)
				measured++
				if a.bulkReceipt.fresh(now) {
					r := a.bulkReceipt.mean + a.bulkReceipt.jitter
					receiptCosts[i] = r
					if receiptMeasured == 0 || r < bestReceipt {
						bestReceipt = r
					}
					receiptMeasured++
				}
			}
			a.mu.Unlock()
		}
		candidates = append(candidates, interactiveRoute{i, cost})
	}
	if measured < 2 {
		return nil
	}
	// Compare only paths with sufficient fresh receipt observations. A bounded
	// relative penalty protects against a common delayed feedback batch starving
	// all paths. The existing rotating exploration remains independent of score.
	if receiptMeasured >= 2 {
		for i := range candidates {
			if r := receiptCosts[candidates[i].index]; r > 0 {
				candidates[i].cost += min(150*time.Millisecond, max(0, r-bestReceipt)/2)
			}
		}
	}
	// Small differences are noise; preserve the rotating order of similar paths.
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].cost/(5*time.Millisecond) < candidates[j].cost/(5*time.Millisecond)
	})
	order := make([]int, len(candidates))
	for i, c := range candidates {
		order[i] = c.index
	}
	return order
}
