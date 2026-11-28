package tunnel

import (
	"context"
	"sort"
	"strings"
	"time"

	"itom.local/core/accountpolicy"
	"itom.local/mailstream"
)

// The bearer selects the cellular policy. Fresh IMAP, queue and delivery
// evidence determine the account limit; the transport pacer remains independent.
func (c *Client) SetNetworkType(kind string) {
	c.mu.Lock()
	c.networkType = strings.ToLower(kind)
	if c.session != nil {
		c.session.SetCellular(c.networkType == "cellular" || c.networkType == "mobile")
	}
	c.mu.Unlock()
}

type cellularPolicy struct {
	base                  float64
	severe, healthy, idle int
	limit                 int
	lastChange            time.Time
	quietTicks            int
}

// Recovery preserves four paths under useful delivery, but never intentionally
// parks below three. No single stale RTT measurement can trigger a resize.
func (p *cellularPolicy) decideRecovery(capacity int, latency float64, demand bool, rate uint64, now time.Time) int {
	return p.decideWithEvidence(capacity, bearerEvidence{median: latency}, demand, rate, now)
}
func (p *cellularPolicy) decideWithEvidence(capacity int, evidence bearerEvidence, demand bool, rate uint64, now time.Time) int {
	latency := evidence.median
	if capacity < 1 {
		return 0
	}
	minimum := min(accountpolicy.MinimumAccounts, capacity)
	floor := min(4, capacity)
	if p.limit == 0 {
		p.limit = capacity
	}
	p.limit = max(minimum, min(p.limit, capacity))
	if latency <= 0 {
		p.healthy = 0
		return p.limit
	}
	if p.base == 0 || latency < p.base {
		p.base = latency
	}
	if demand && rate > 0 {
		p.quietTicks = 0
	} else {
		p.quietTicks++
	}
	bad := latency >= 250 || latency > max(100, 3*p.base) || evidence.congested
	good := latency <= max(120, 1.5*p.base) && !evidence.congested && !evidence.volatile
	if bad {
		p.severe++
		p.healthy = 0
	} else {
		p.severe = 0
		if good || (demand && rate > 0 && latency < 400 && !evidence.volatile) {
			p.healthy++
		} else {
			p.healthy = 0
		}
	}
	if demand {
		p.idle = 0
	} else {
		p.idle++
	}
	// Recovery below the normal four-path floor should not be gated by
	// the slower downscale interval. Only genuine demand and healthy IMAP
	// allow an additional path; congestion downscale still waits 5 seconds.
	if demand && p.limit < floor && good && p.healthy >= 2 && (p.lastChange.IsZero() || now.Sub(p.lastChange) >= 2*time.Second) {
		p.limit++
		p.lastChange = now
		p.healthy, p.severe = 0, 0
		return p.limit
	}
	if !p.lastChange.IsZero() && now.Sub(p.lastChange) < 5*time.Second {
		return p.limit
	}
	old := p.limit
	switch {
	case p.severe >= 3 && (latency >= 700 || evidence.stalled) && p.quietTicks >= 3:
		p.limit = max(minimum, p.limit-1)
	case demand && rate > 0 && p.limit < floor && latency < 700 && p.healthy >= 2:
		p.limit++
	case p.severe >= 2 && p.limit > floor:
		p.limit = max(floor, p.limit-1)
	case demand && p.healthy >= 2:
		p.limit = min(capacity, p.limit+1)
	case p.idle >= 12:
		// Keep three usable paths even when idle or on a degraded bearer.
		if p.limit > minimum {
			p.limit--
		}
		p.idle = 0
	}
	if old != p.limit {
		p.lastChange = now
		p.healthy = 0
		p.severe = 0
	}
	return p.limit
}

func (c *Client) accountController(ctx context.Context) {
	defer c.workers.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var policy cellularPolicy
	var previous uint64
	var rateAt time.Time
	var history bearerHistory
	degraded := false
	var wasMobile bool
	var demandUntil time.Time
	last := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		c.mu.Lock()
		mobile := c.networkType == "cellular" || c.networkType == "mobile"
		c.mu.Unlock()
		mail := c.link.Snapshot()
		if !mail.GrantNegotiated || mail.GrantChanging || mail.AllocatedAccounts == 0 {
			continue
		}
		if mobile != wasMobile {
			policy = cellularPolicy{}
			history = bearerHistory{}
			previous = 0
			rateAt = time.Time{}
			demandUntil = time.Time{}
			last = -1
			wasMobile = mobile
		}
		var latency []float64
		active := 0
		for _, line := range mail.Lines {
			if line.Allocated && line.Ready && line.Data && line.Writer && line.ControlWriter && line.Failure == "" && !line.PeerUnavailable {
				active++
				if line.IMAPLatencyFresh && line.IMAPLatencyMillis > 0 {
					latency = append(latency, float64(line.IMAPLatencyMillis))
				}
			}
		}
		desired := 0
		target := mail.AllocatedAccounts
		if mobile && mail.Accounts > accountpolicy.SharedProfileMaximum {
			sort.Float64s(latency)
			var sample float64
			if len(latency) > 0 {
				sample = latency[len(latency)/2]
			}
			snap := c.session.Snapshot()
			now := time.Now()
			total := snap.BulkDelivered + snap.ReceivedPayload
			var rate uint64
			if !rateAt.IsZero() && total >= previous && now.Sub(rateAt) > 0 {
				rate = uint64(float64(total-previous) / now.Sub(rateAt).Seconds())
			}
			previous, rateAt = total, now
			// At a collapsed rate, a demand threshold proportional to the
			// healthy flight/window is unreachable. Preserve active transfers
			// across sparse receipt rounds, without treating idle TCP sockets
			// alone as demand.
			if cellularTrafficDemand(snap, rate) {
				demandUntil = now.Add(8 * time.Second)
			}
			evidence := history.observe(latency, sample, snap, rate)
			desired = policy.decideWithEvidence(target, evidence, now.Before(demandUntil), rate, now)
			target = desired
		}
		// Physical outages can make the minimum temporarily impossible. Request
		// all assigned paths for repair instead of parking another healthy path.
		if active < accountpolicy.MinimumAccounts {
			desired, target = 0, mail.AllocatedAccounts
			if !degraded {
				c.RecordNetworkEvent("ACCOUNT_MINIMUM_DEGRADED", int64(active))
				degraded = true
			}
		} else if degraded {
			c.RecordNetworkEvent("ACCOUNT_MINIMUM_RESTORED", int64(active))
			degraded = false
		}
		if target == active && last == desired {
			continue
		}
		// Unknown and wired bearers keep all allocated accounts. Persist that limit
		// even if the physical count already matches, for future grant expansion.
		change, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := c.session.SetActiveLimit(change, desired)
		cancel()
		if err == nil {
			last = desired
			c.RecordNetworkEvent("ACCOUNT_LIMIT_CHANGED", int64(target))
		}
	}
}

// Demand is deliberately independent of the current learned rate. A
// 50 kbps transfer must not be treated as an idle bearer forever.
func cellularTrafficDemand(snap mailstream.Snapshot, rate uint64) bool {
	return snap.BulkOutstanding >= 16<<10 || (snap.PeerDemandFresh && snap.PeerBulkPending >= 16<<10) || (snap.Streams > 0 && rate >= 8<<10) || (snap.BulkWindowLimited && snap.PendingBytes > 0)
}

// Only fully attached, healthy data paths count toward the runtime floor.
func usableAccounts(mail mailstream.MailSnapshot) int {
	n := 0
	for _, line := range mail.Lines {
		if line.Allocated && line.Ready && line.Data && line.Writer && line.ControlWriter && line.Failure == "" && !line.PeerUnavailable {
			n++
		}
	}
	return n
}

type bearerEvidence struct {
	median, tail, jitter         float64
	volatile, congested, stalled bool
}
type bearerHistory struct {
	median                float64
	rate, retransmissions uint64
	initialized           bool
}

// Instantaneous tail/spread and successive deltas provide faster evidence than
// a mean alone. Corroborate latency spikes with actual delivery or queue stress;
// deliberate pacing and sparse receipts must not alone trigger a downscale.
func (h *bearerHistory) observe(sorted []float64, median float64, snap mailstream.Snapshot, rate uint64) bearerEvidence {
	e := bearerEvidence{median: median}
	if len(sorted) > 0 {
		e.tail = sorted[(9*len(sorted)+9)/10-1]
		e.jitter = e.tail - sorted[0]
	}
	delta := median - h.median
	if delta < 0 {
		delta = -delta
	}
	e.volatile = median > 0 && (e.jitter > max(80, median) || h.initialized && delta > max(70, h.median))
	demand := cellularTrafficDemand(snap, rate)
	loss := h.initialized && snap.Retransmissions > h.retransmissions
	falling := h.initialized && h.rate >= 64<<10 && rate < h.rate/3
	pressure := snap.InteractiveQueueLimited || snap.MobileDraining || snap.PeerDemandFresh && snap.PeerDraining
	e.stalled = demand && rate == 0 && (snap.MobileDraining || snap.PeerDemandFresh && snap.PeerDraining)
	e.congested = median > 0 && (e.tail >= 250 || e.volatile) && demand && (falling || loss || pressure) || e.stalled
	h.median, h.rate, h.retransmissions, h.initialized = median, rate, snap.Retransmissions, true
	return e
}
