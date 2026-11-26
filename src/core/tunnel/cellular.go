package tunnel

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Bearer is merely a policy selector. Only fresh provider IMAP latency
// determines link quality; application RTT and packet loss do not enter it.
func (c *Client) SetNetworkType(kind string) {
	c.mu.Lock()
	c.networkType = strings.ToLower(kind)
	c.mu.Unlock()
}

type cellularPolicy struct {
	base                  float64
	severe, healthy, idle int
	limit                 int
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
func (c *Client) accountController(ctx context.Context) {
	defer c.workers.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var policy cellularPolicy
	var previous uint64
	var wasMobile bool
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
			demand := c.session.Snapshot().PendingBytes >= 256<<10 || rate >= uint64(max(active, 1))*250000
			desired = policy.decide(target, sample, demand)
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
