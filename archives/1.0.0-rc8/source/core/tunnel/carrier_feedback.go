package tunnel

import (
	"itom.local/core/datapath"
	"sort"
	"time"
)

// Missing/stale echo measurements use the fresh pool median. Estimates never
// authorize publication or change physical quota or unknown APPEND debt.
func (g *stripeGroup) carrierLatencyLocked(l *lane) int64 {
	now := time.Now().UnixMilli()
	sample := func(c *lane) int64 {
		if c == nil {
			return 0
		}
		// Classify path propagation using unloaded samples. A busy pool's
		// queue-inflated ping must not make a slow backend look comparable to
		// healthy but temporarily busy readers. Current RTT remains diagnostic;
		// real queued bytes/slots and signed ACK ages still drive placement cost.
		if at := c.idleRTTAt.Load(); at > 0 && now >= at && now-at <= 120000 {
			if idle := c.idleRTT.Load(); idle > 0 {
				return min(int64(2500), idle)
			}
		}
		if g.active.Load() > 0 {
			return 0
		}
		at := c.rttAt.Load()
		if at > 0 && (now < at || now-at > 15000) {
			return 0
		}
		rtt := c.smoothedRTT.Load()
		if rtt == 0 {
			rtt = c.rtt.Load()
		}
		return min(int64(2500), max(int64(0), rtt))
	}
	if value := sample(l); value > 0 {
		return value
	}
	var values []int64
	for _, c := range g.lanes {
		if v := sample(c); v > 0 {
			values = append(values, v)
		}
	}
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[(len(values)-1)/2]
}

// Only fresh, positive loaded-RTT spikes affect short/interactive placement.
// An elevated busy RTT may reflect transient queueing rather than propagation.
// This advisory cost never excludes a carrier or changes authenticated credit,
// physical quota, journal ownership or in-flight TCP socket identity.
func fastLoadedRTTSpikePenalty(sampleAgeMS, currentMS, smoothedMS int64) int {
    if sampleAgeMS < 0 || sampleAgeMS > 10000 || currentMS <= 0 || smoothedMS <= 0 {
        return 0
    }
    excess := currentMS - smoothedMS - 40 // ignore normal sample variation
    if excess <= 0 { return 0 }
    return int(min(int64(240), excess * 2))
}

func (g *stripeGroup) fastLoadedRTTSpikePenaltyLocked(l *lane) int {
    if l == nil { return 0 }
    now := time.Now().UnixMilli()
    return fastLoadedRTTSpikePenalty(now-l.rttAt.Load(), l.rtt.Load(), l.smoothedRTT.Load())
}

// A short burst should await a responsive reader's slot before spilling onto
// an idle but much slower account. This is relative to the measured pool, so
// high mobile RTT alone excludes no path. Selection uses a bounded grace and
// still falls back if responsive accounts cannot supply physical capacity.
func (g *stripeGroup) fastLatencyCeilingLocked() int64 {
	best := int64(1 << 62)
	for _, l := range g.lanes {
		if l == nil || !l.streamCarrierReady() || l.ctx.Err() != nil || physicalSlotLimit(l) == 0 || l.controlDebt.Load() >= 80 || l.cleanupBlocksFast() {
			continue
		}
		if m, ok := l.mail.(interface{ DataAvailable() bool }); ok && !m.DataAvailable() {
			continue
		}
		best = min(best, g.carrierLatencyLocked(l))
	}
	if best == 1<<62 {
		return best
	}
	return best + max(int64(250), best/2)
}

// Keep enough measured RTT budget for the wire round trip *after* carrier
// selection. A congested preferred reader should not hold a short request
// for hundreds of milliseconds before an otherwise usable carrier is tried.
func boundedInteractiveGrace(bestMS, outlierMS int64) time.Duration {
	if bestMS <= 0 || outlierMS <= bestMS {
		return 100 * time.Millisecond
	}
	// A smaller difference makes waiting unhelpful. An outlier that is much
	// slower may still warrant one short (at most 150ms) preferential turn.
	difference := time.Duration(outlierMS-bestMS) * time.Millisecond / 3
	wireBudget := time.Duration(max(int64(0), 750-bestMS)) * time.Millisecond / 4
	return max(25*time.Millisecond, min(150*time.Millisecond, difference, wireBudget))
}

func (g *stripeGroup) fastSlotGraceLocked() time.Duration {
	ceiling := g.fastLatencyCeilingLocked()
	best, outlier := int64(1<<62), int64(1<<62)
	for _, l := range g.lanes {
		if l == nil || !l.streamCarrierReady() || l.ctx.Err() != nil || physicalSlotLimit(l) == 0 || l.controlDebt.Load() >= 80 || l.cleanupBlocksFast() {
			continue
		}
		if m, ok := l.mail.(interface{ DataAvailable() bool }); ok && !m.DataAvailable() {
			continue
		}
		rtt := g.carrierLatencyLocked(l)
		best = min(best, rtt)
		if rtt > ceiling {
			outlier = min(outlier, rtt)
		}
	}
	if outlier == 1<<62 {
		return 100 * time.Millisecond
	}
	// Even a healthy 300-500ms path cannot afford a 650ms additional wait
	// for physical grant availability within the 750ms application RTT budget.
	// This is only a scheduling preference: no grant, TCP stream, journal
	// record or logical credit is discarded when the brief grace expires.
	return boundedInteractiveGrace(best, outlier)
}

// One short stream can return on its actual incoming physical reader. A batch
// spanning different streams with different affinities retains pool balancing.
func (g *stripeGroup) shortTarget(jobs []*datapath.Job) *lane {
	g.mu.Lock()
	defer g.mu.Unlock()
	var account string
	for _, j := range jobs {
		id := g.shortAffinity[j.Attempt().Stream]
		if id == "" || account != "" && account != id {
			return nil
		}
		account = id
	}
	return g.lanes[account]
}

// Affinity is a small preference among responsive paths. Congestion and atomic
// grant/slot eligibility always take precedence. Caller holds g.mu.
func (g *stripeGroup) preferShortTargetLocked(keys []string, preferred *lane) {
	if preferred == nil || len(keys) < 2 {
		return
	}
	score := func(l *lane) int {
		// The peer can choose a different outgoing reserve. Compare actual
		// latency/backlog here, without our own reserve's artificial bonus.
		n := interactiveCarrierScore(l, [2]*lane{}) + int(g.carrierLatencyLocked(l)) + g.fastLoadedRTTSpikePenaltyLocked(l)
		if pending := g.delivery.fastCarrierRanges[l.account.ID]; pending > 0 {
			// A serial reply can arrive before its preceding cumulative ACK.
			// Retain a small cost for that charged range, while a parallel burst
			// still becomes progressively less preferable than a free sibling.
			n += pending * 32
		}
		return n
	}
	for i, key := range keys {
		l := g.lanes[key]
		if l == preferred && l.streamCarrierReady() && l.ctx.Err() == nil && score(l) <= score(g.lanes[keys[0]])+75 {
			copy(keys[1:i+1], keys[:i])
			keys[0] = key
			return
		}
	}
}
