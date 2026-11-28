package tunnel

import (
	"context"
	"sort"
	"strings"
	"time"

	"itom.local/mailstream"
)

// Bearer is merely a policy selector. Only fresh provider IMAP latency
// determines link quality; application RTT and packet loss do not enter it.
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

func (p *cellularPolicy) decide(capacity int, latency float64, demand bool) int {
	if capacity < 1 {
		return 0
	}
	if p.limit == 0 {
		p.limit = min(2, capacity)
	}
	p.limit = min(p.limit, capacity)
	if latency <= 0 {
		return p.limit
	}
	if p.base == 0 || latency < p.base {
		p.base = latency
	}
	bad := latency >= 250 || latency > max(100, 3*p.base)
	good := latency <= max(120, 1.5*p.base)
	if bad {
		p.severe++
		p.healthy = 0
		p.idle = 0
	} else {
		p.severe = 0
		if good {
			p.healthy++
		} else {
			p.healthy = 0
		}
		if demand {
			p.idle = 0
		} else {
			p.idle++
		}
	}
	switch {
	case p.severe >= 2:
		p.limit = 1
	case demand && p.healthy >= 2:
		p.limit = min(capacity, max(p.limit+1, p.limit*2))
		p.healthy = 0
	case p.idle >= 12:
		p.limit = max(1, p.limit-1)
		p.idle = 0
	}
	return p.limit
}

// Preserve four useful paths while a loaded bearer is still delivering.
// Step down serially; permit one path during sustained severe IMAP delay
// without progress, or when idle. Never exceed the negotiated grant.
func (p *cellularPolicy) decideRecovery(capacity int, latency float64, demand bool, rate uint64, now time.Time) int {
	if capacity < 1 {
		return 0
	}
	floor := min(4, capacity)
	if p.limit == 0 {
		p.limit = floor
	}
	p.limit = min(p.limit, capacity)
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
	bad := latency >= 250 || latency > max(100, 3*p.base)
	good := latency <= max(120, 1.5*p.base)
	if bad {
		p.severe++
		p.healthy = 0
	} else {
		p.severe = 0
		if good || (demand && rate > 0 && latency < 400) {
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
	case p.severe >= 3 && latency >= 700 && p.quietTicks >= 3:
		p.limit = max(1, p.limit-1)
	case demand && rate > 0 && p.limit < floor && latency < 700 && p.healthy >= 2:
		p.limit++
	case p.severe >= 2 && p.limit > floor:
		p.limit = max(floor, p.limit-1)
	case demand && p.healthy >= 2:
		p.limit = min(capacity, p.limit+1)
	case p.idle >= 12:
		// One path is reserved for genuinely poor bearer conditions, not
		// an ordinary idle period on a healthy radio. Never increase a
		// severely reduced grant merely because the application is idle.
		if p.limit > min(2, capacity) {
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
			demandUntil = time.Time{}
			last = -1
			wasMobile = mobile
		}
		var total uint64
		var latency []float64
		active := 0
		for _, line := range mail.Lines {
			total += line.UploadBytes + line.DownloadBytes
			if line.Ready && line.Data && line.Writer && line.ControlWriter {
				active++
				if line.IMAPLatencyFresh && line.IMAPLatencyMillis > 0 {
					latency = append(latency, float64(line.IMAPLatencyMillis))
				}
			}
		}
		var rate uint64
		if previous != 0 && total >= previous {
			rate = total - previous
		}
		previous = total
		desired := 0
		target := mail.AllocatedAccounts
		if mobile {
			sort.Float64s(latency)
			var sample float64
			if len(latency) > 0 {
				sample = latency[len(latency)/2]
			}
			snap := c.session.Snapshot()
			now := time.Now()
			// At a collapsed rate, a demand threshold proportional to the
			// healthy flight/window is unreachable. Preserve active transfers
			// across sparse receipt rounds, without treating idle TCP sockets
			// alone as demand.
			if cellularTrafficDemand(snap, rate) {
				demandUntil = now.Add(8 * time.Second)
			}
			desired = policy.decideRecovery(target, sample, now.Before(demandUntil), rate, now)
			target = desired
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
