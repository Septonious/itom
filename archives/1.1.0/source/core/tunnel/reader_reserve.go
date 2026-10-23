package tunnel

import (
	"sort"
	"time"
)

// Parallel setup traffic may choose a different physical return path for each
// batch. While eight authenticated fast ranges are still outstanding, avoid
// repeatedly entering IDLE on siblings between short pages. A single RPC or
// idle PING never enables pool-wide polling; delivery ACKs end the burst.
func (g *stripeGroup) parallelFastTraffic() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	now := time.Now()
	for _, r := range g.delivery.ranges {
		if !r.fast || r.carrier == "" || now.Sub(r.admitted) > 2*time.Second {
			continue
		}
		n++
		if n >= 8 {
			return true
		}
	}
	return false
}

// Only client short DATA expects a fresh reply. Prime the deterministic quiet
// receive reserve across physical stripes, not just the logical owner's reader.
// Reply DATA and repairs must not create reciprocal polling storms.
func (g *stripeGroup) expectFastResponse(owner *lane) {
	g.expectFastResponseOn(owner, nil)
}

func (g *stripeGroup) expectFastResponseOn(owner, physical *lane) {
	if g == nil || owner == nil || owner.side {
		return
	}
	g.mu.Lock()
	quiet := g.interactiveReadersLocked()
	g.mu.Unlock()
	budget := owner.readerResponseBudget(650 * time.Millisecond)
	if quiet[0] == nil {
		owner.armReaderHot(budget)
	} else {
		for _, l := range quiet {
			if l != nil {
				l.armReaderHot(budget)
			}
		}
	}
	// Parallel cold startups can spill beyond the two reserved readers. Prime
	// the actual request carrier too, while retaining bounded response polling.
	if physical != nil && physical != quiet[0] && physical != quiet[1] && physical.ctx.Err() == nil {
		physical.armReaderHot(budget)
	}
}

func (l *lane) readerResponseBudget(base time.Duration) time.Duration {
	if rtt := l.rtt.Load(); rtt > 0 {
		base = max(base, time.Duration(min(rtt, 1000))*2*time.Millisecond+100*time.Millisecond)
	}
	return min(base, 2*time.Second)
}

func (l *lane) armReaderHot(budget time.Duration) {
	now := time.Now()
	until := now.Add(budget)
	extendReaderHot(&l.readHotUntil, until.UnixNano())
	if hot, ok := l.mail.(interface{ SetReaderHot(time.Time) }); ok {
		hot.SetReaderHot(until)
	}
	l.readHotGeneration.Add(1)
	// A request is fresh demand even inside an existing response window. Wake
	// the current empty poll as well as IDLE; the one-slot signal coalesces bursts.
	// Checking only the old expiry can also race entry into IDLE at that expiry.
	l.wakeReader()
}

// Protect two responsive BODY paths from new bulk. Smoothed fresh echoes select
// the outgoing reserve with jitter hysteresis. Temporary control/writer debt
// never rotates it into current bulk. Short affinity primes the actual return
// path when the other endpoint chooses a different outgoing reserve.
// Caller holds g.mu; existing physical reservations finish normally.
func (g *stripeGroup) interactiveReadersLocked() [2]*lane {
	var best [2]*lane
	var candidates []*lane
	for _, l := range g.lanes {
		if l == nil || !l.streamCarrierReady() || l.ctx.Err() != nil || physicalSlotLimit(l) == 0 {
			continue
		}
		if available, ok := l.mail.(interface{ DataAvailable() bool }); ok && !available.DataAvailable() {
			continue
		}
		candidates = append(candidates, l)
	}
	if len(candidates) < 5 {
		g.interactiveReserve = [2]string{}
		return best
	}
	count := 2
	// A split deployment needs only one protected DATA receiver. Keep its
	// account order symmetric so the request primes the actual return path.
	// Mixed layouts retain their original two-reader policy.
	for _, l := range candidates {
		if l.dataShards > 0 {
			count = 1
			break
		}
	}
	if g.delivery.bulk > 0 {
		for _, l := range candidates {
			for i, id := range g.interactiveReserve {
				if l.account.ID == id {
					best[i] = l
				}
			}
		}
		// Do not make a loaded ping turn a current bulk BODY reader into a
		// supposedly empty reserve. Re-evaluate only after logical bulk drains;
		// disappearance of a carrier still permits immediate replacement.
		if best[0] != nil && (count == 1 || best[1] != nil) {
			if count == 1 {
				best[1] = nil
			}
			return best
		}
		best = [2]*lane{}
	}
	scores := map[*lane]int64{}
	for _, l := range candidates {
		scores[l] = g.carrierLatencyLocked(l)
		if count == 1 {
			scores[l] = 0
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if scores[candidates[i]] == scores[candidates[j]] {
			return candidates[i].account.ID < candidates[j].account.ID
		}
		return scores[candidates[i]] < scores[candidates[j]]
	})
	for i := 0; i < count; i++ {
		chosen := candidates[0]
		for _, l := range candidates {
			if l.account.ID == g.interactiveReserve[i] && scores[l] <= scores[chosen]+max(int64(100), scores[chosen]/4) {
				chosen = l
				break
			}
		}
		best[i] = chosen
		g.interactiveReserve[i] = chosen.account.ID
		for j, l := range candidates {
			if l == chosen {
				candidates = append(candidates[:j], candidates[j+1:]...)
				break
			}
		}
	}
	return best
}
