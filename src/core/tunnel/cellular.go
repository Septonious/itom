package tunnel

import (
	"context"
	"strings"
	"time"
)

// Network changes invalidate samples from the old bearer, including probes
// already in flight. Only fresh application RTT determines reductions.
func (c *Client) SetNetworkType(kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kind = strings.ToLower(kind)
	if c.networkType != kind {
		c.networkType = kind
		c.rttEpoch++
		c.rttHistory = nil
	}
}

const cellularRTTWindow = 10 * time.Second

type rttSample struct {
	at    time.Time
	value time.Duration
}

func trimRTT(samples []rttSample, now time.Time) []rttSample {
	cutoff := now.Add(-cellularRTTWindow)
	n := 0
	for _, s := range samples {
		if s.at.After(cutoff) {
			samples[n] = s
			n++
		}
	}
	return samples[:n]
}
func (c *Client) cellularRTT(now time.Time) (time.Duration, int, uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	mobile := c.networkType == "cellular" || c.networkType == "mobile"
	average, n := c.cellularRTTLocked(now)
	return average, n, c.rttEpoch, mobile
}

func (c *Client) cellularRTTLocked(now time.Time) (time.Duration, int) {
	c.rttHistory = trimRTT(c.rttHistory, now)
	var sum time.Duration
	for _, s := range c.rttHistory {
		sum += s.value
	}
	n := len(c.rttHistory)
	if n == 0 {
		return 0, 0
	}
	return sum / time.Duration(n), n
}

type cellularPolicy struct {
	healthy      int
	limit        int
	lastDecrease time.Time
}

// Only decrease on a ten-second rolling application RTT average, never
// because of provider IMAP service latency or idle traffic. Apply at most
// one reduction per window so overlapping measurements cannot cause collapse.
func (p *cellularPolicy) decide(capacity int, average time.Duration, samples int, demand bool, now time.Time) int {
	if capacity < 1 {
		return 0
	}
	if p.limit == 0 {
		p.limit = capacity
	}
	p.limit = min(p.limit, capacity)
	if samples < 2 {
		p.healthy = 0
		return p.limit
	}
	if average <= 400*time.Millisecond {
		p.healthy++
		if demand && p.healthy >= 2 && (p.lastDecrease.IsZero() || now.Sub(p.lastDecrease) >= cellularRTTWindow) {
			p.limit = min(capacity, max(p.limit+1, p.limit*2))
			p.healthy = 0
		}
		return p.limit
	}
	p.healthy = 0
	if !p.lastDecrease.IsZero() && now.Sub(p.lastDecrease) < cellularRTTWindow {
		return p.limit
	}
	previous := p.limit
	switch {
	case average >= 700*time.Millisecond:
		p.limit = max(1, p.limit/2)
	case average > 600*time.Millisecond:
		p.limit = max(1, p.limit-max(2, (p.limit+2)/3))
	default:
		p.limit = max(1, p.limit-1)
	}
	if previous != p.limit {
		p.lastDecrease = now
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
	var epoch uint64
	var bearerSince time.Time
	last := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		mail := c.link.Snapshot()
		if !mail.GrantNegotiated || mail.GrantChanging || mail.AllocatedAccounts == 0 {
			continue
		}
		now := time.Now()
		average, samples, currentEpoch, mobile := c.cellularRTT(now)
		if mobile != wasMobile || currentEpoch != epoch {
			policy = cellularPolicy{}
			last = -1
			wasMobile = mobile
			epoch = currentEpoch
			bearerSince = now
			previous = 0
		}
		var total uint64
		active := 0
		for _, line := range mail.Lines {
			total += line.UploadBytes + line.DownloadBytes
			if line.Ready && line.Data && line.Writer && line.ControlWriter {
				active++
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
			demand := c.session.Snapshot().PendingBytes >= 256<<10 || rate >= uint64(max(active, 1))*250000
			// Wait for a complete window on a new bearer before reducing.
			if now.Sub(bearerSince) < cellularRTTWindow {
				samples = 0
			}
			desired = policy.decide(target, average, samples, demand, now)
			target = desired
		}
		c.mu.Lock()
		changedBearer := c.rttEpoch != epoch
		if !changedBearer {
			c.cellularDesired = desired
		}
		c.mu.Unlock()
		if changedBearer {
			continue
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
