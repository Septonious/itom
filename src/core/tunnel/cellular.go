package tunnel

import (
	"context"
	"sort"
	"strings"
	"time"

	"itom.local/core/servercfg"
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
		c.rttSince = time.Now()
		if c.session != nil {
			c.session.SetCellular(kind == "cellular" || kind == "mobile")
		}
	}
}

const cellularRTTWindow = 10 * time.Second

type mailRate struct{ up, down, control uint64 }

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
	healthy            int
	limit              int
	lastChange         time.Time
	beforeReduction    int
	rttBeforeReduction time.Duration
}

// Only decrease on a ten-second rolling application RTT average, never
// because of provider IMAP service latency or idle traffic. Apply at most
// one reduction per window so overlapping measurements cannot cause collapse.
func (p *cellularPolicy) decide(capacity int, average time.Duration, samples int, demand bool, now time.Time) int {
	if capacity < servercfg.MinOperationalAccounts {
		return 0 // invalid allocation, not permission to use a smaller operating limit
	}
	if p.limit == 0 {
		p.limit = capacity
	}
	p.limit = min(capacity, max(servercfg.MinOperationalAccounts, p.limit))
	if samples < 2 {
		p.healthy = 0
		return p.limit
	}
	if !p.lastChange.IsZero() && now.Sub(p.lastChange) < cellularRTTWindow {
		return p.limit
	}
	previous := p.limit
	if average <= 400*time.Millisecond {
		p.healthy++
		if demand && p.healthy >= 2 {
			percent := 10
			if average <= 300*time.Millisecond {
				percent = 20
			}
			p.limit = min(capacity, p.limit+accountStep(capacity, percent))
			if p.limit != previous {
				p.lastChange = now
			}
			p.healthy = 0
		}
		return p.limit
	}
	p.healthy = 0
	switch {
	case average >= 700*time.Millisecond:
		p.limit = max(servercfg.MinOperationalAccounts, p.limit-accountStep(capacity, 35))
	case average > 600*time.Millisecond:
		p.limit = max(servercfg.MinOperationalAccounts, p.limit-accountStep(capacity, 20))
	default:
		p.limit = max(servercfg.MinOperationalAccounts, p.limit-accountStep(capacity, 10))
	}
	if previous != p.limit {
		p.lastChange = now
	}
	return p.limit
}

// Account-count decisions must distinguish shared bearer delay from mailbox
// pressure. Measured APPEND service is advisory, not independent radio capacity.
type cellularEvidence struct {
	active, streams, queued, slots, busy  int
	serviceCapacity, publications         float64
	bulkPressure, retries, timeout, fresh bool
}

func (e cellularEvidence) overloaded() bool {
	return e.retries || e.queued > max(2, e.active) || (e.slots > 0 && e.busy*4 >= e.slots*3 && e.queued > 0) || (e.serviceCapacity > 0 && e.publications >= .8*e.serviceCapacity)
}
func (e cellularEvidence) minimum(capacity int) int {
	// At least four, plus enough service headroom for observed completions.
	// Completion rate is a lower bound on demand when queues are growing.
	n := servercfg.MinOperationalAccounts
	if e.serviceCapacity > 0 && e.active > 0 {
		for n < capacity && e.publications > .7*e.serviceCapacity*float64(n)/float64(e.active) {
			n++
		}
	}
	return min(capacity, n)
}
func (p *cellularPolicy) decideObserved(capacity int, average time.Duration, samples int, demand bool, now time.Time, e cellularEvidence) int {
	if capacity < servercfg.MinOperationalAccounts {
		return 0
	}
	if p.limit == 0 {
		p.limit = capacity
	}
	p.limit = min(capacity, max(servercfg.MinOperationalAccounts, p.limit))
	if !p.lastChange.IsZero() && now.Sub(p.lastChange) < cellularRTTWindow {
		return p.limit
	}
	previous := p.limit
	// Restore the last operating point if a reduction made no material
	// improvement. Timeout/command pressure can expand even at >700ms.
	if p.beforeReduction > p.limit && samples >= 2 && average >= 400*time.Millisecond && average >= p.rttBeforeReduction*9/10 {
		p.limit = min(capacity, p.beforeReduction)
		p.beforeReduction = 0
	} else if (e.timeout || e.overloaded()) && (e.streams > 0 || e.queued > 0) {
		p.limit = min(capacity, p.limit+accountStep(capacity, 20))
		p.beforeReduction = 0
	} else if e.active < servercfg.MinOperationalAccounts {
		p.limit = min(capacity, max(p.limit, servercfg.MinOperationalAccounts))
	} else if average > 400*time.Millisecond {
		// High RTT alone, especially while idle/stalled, is not evidence
		// that reducing physical accounts will improve radio service.
		if samples < 2 || !e.fresh || !e.bulkPressure || e.overloaded() {
			p.healthy = 0
			return p.limit
		}
		p.decide(capacity, average, samples, demand, now)
		p.limit = max(p.limit, e.minimum(capacity))
		if p.limit < previous {
			p.beforeReduction, p.rttBeforeReduction = previous, average
		}
	} else {
		p.decide(capacity, average, samples, demand, now)
		if p.limit > previous || (samples >= 2 && average <= 300*time.Millisecond) {
			p.beforeReduction = 0
		}
	}
	if p.limit != previous {
		p.lastChange = now
		p.healthy = 0
	}
	return p.limit
}
func canLearnCellularBaseline(e cellularEvidence, rtt int64, demand bool, settled bool) bool {
	return settled && e.fresh && !e.timeout && !e.retries && !e.overloaded() && !demand && e.streams == 0 && e.queued == 0 && rtt > 0 && rtt <= 400
}
func accountStep(capacity, percent int) int { return max(1, (capacity*percent+50)/100) }

// Robust idle baseline indexed by the actual live count. It is advisory;
// raw RTT remains the threshold for reducing accounts.
type cellularBaseline struct{ values []time.Duration }

func (b *cellularBaseline) add(v time.Duration) {
	if v <= 0 {
		return
	}
	b.values = append(b.values, v)
	if len(b.values) > 31 {
		b.values = b.values[len(b.values)-31:]
	}
}
func (b *cellularBaseline) estimate() (time.Duration, time.Duration) {
	if len(b.values) < 5 {
		return 0, 0
	}
	v := append([]time.Duration(nil), b.values...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	median := v[len(v)/2]
	for i, x := range v {
		if x < median {
			v[i] = median - x
		} else {
			v[i] = x - median
		}
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return median, time.Duration(float64(v[len(v)/2]) * 1.4826)
}
func (c *Client) accountController(ctx context.Context) {
	defer c.workers.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var policy cellularPolicy
	var previous = map[string]mailRate{}
	var baselines = map[int]*cellularBaseline{}
	var sampled time.Time
	var lastActive int
	var lastProbe int64
	var previousRetries, previousBackpressure uint64
	var lastPressure time.Time
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
		if !mail.GrantNegotiated || mail.GrantChanging || mail.AllocatedAccounts < servercfg.MinOperationalAccounts {
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
			previous = map[string]mailRate{}
			baselines = map[int]*cellularBaseline{}
			sampled = time.Time{}
			lastActive = 0
		}
		active := 0
		evidence := cellularEvidence{}
		var up, down uint64
		elapsed := now.Sub(sampled).Seconds()
		for _, line := range mail.Lines {
			old, ok := previous[line.Account]
			if line.Ready && line.Data && line.Writer && line.ControlWriter {
				active++
				evidence.queued += line.ControlQueued
				evidence.slots += line.InteractiveSlots
				evidence.busy += line.InteractiveBusy
				if line.ControlPublicationMeanUS > 0 && line.ControlServiceAgeMS >= 0 && line.ControlServiceAgeMS <= 10000 {
					evidence.serviceCapacity += float64(line.InteractiveSlots) * 1e6 / float64(line.ControlPublicationMeanUS+max(0, line.ControlPublicationJitterUS))
				}
				if ok && !sampled.IsZero() && elapsed > 0 && line.ControlAppended >= old.control {
					evidence.publications += float64(line.ControlAppended-old.control) / elapsed
				}
				if ok && !sampled.IsZero() && elapsed > 0 && line.UploadBytes >= old.up && line.DownloadBytes >= old.down {
					up += uint64(float64(line.UploadBytes-old.up) * 8 / elapsed)
					down += uint64(float64(line.DownloadBytes-old.down) * 8 / elapsed)
				}
			}
			previous[line.Account] = mailRate{line.UploadBytes, line.DownloadBytes, line.ControlAppended}
		}
		sampled = now
		if active != lastActive {
			bearerSince = now
			lastActive = active
			samples = 0
			c.mu.Lock()
			if c.rttEpoch == epoch {
				c.rttHistory = nil
				c.rttSince = now
			}
			c.mu.Unlock()
		}
		// Physical byte counters include hedges/control; report effective
		// shares, never use them as independent capacity measurements.
		snapshot := c.session.Snapshot()
		demand := snapshot.PendingBytes >= 128<<10 || snapshot.DeliveryFlight >= 128<<10 || max(up, down) >= 1_000_000
		probe := c.Probe()
		evidence.active, evidence.streams = active, snapshot.Streams
		evidence.fresh = probe.Failure == "" && probe.SampledAtMS > 0 && now.UnixMilli()-probe.SampledAtMS < 3000
		evidence.timeout = probe.Failure != "" || (probe.SampledAtMS > 0 && now.UnixMilli()-probe.SampledAtMS > 6000)
		if snapshot.Retransmissions > previousRetries || snapshot.ReceiptBackpressure > previousBackpressure {
			lastPressure = now
		}
		previousRetries, previousBackpressure = snapshot.Retransmissions, snapshot.ReceiptBackpressure
		evidence.retries = !lastPressure.IsZero() && now.Sub(lastPressure) < cellularRTTWindow
		evidence.bulkPressure = snapshot.BulkDeliveryFlight >= 128<<10 && snapshot.MobileGuarded && snapshot.BulkWindowLimited
		settled := now.Sub(bearerSince) >= cellularRTTWindow
		base := baselines[active]
		if base == nil {
			base = &cellularBaseline{}
			baselines[active] = base
		}
		if active >= servercfg.MinOperationalAccounts && probe.SampledAtMS != lastProbe && canLearnCellularBaseline(evidence, probe.RTTMillis, demand, settled) {
			base.add(time.Duration(probe.RTTMillis) * time.Millisecond)
		}
		lastProbe = probe.SampledAtMS
		median, sigma := base.estimate()
		// Once fresh RTT has recovered, don't remove more accounts based on
		// older backlog. Raw 10s thresholds still govern sustained reductions.
		if average > 400*time.Millisecond && probe.Failure == "" && now.UnixMilli()-probe.SampledAtMS < 3000 && probe.RTTMillis <= 300 {
			samples = 0
		}
		c.mu.Lock()
		c.cellularUpBPS, c.cellularDownBPS = up/uint64(max(1, active)), down/uint64(max(1, active))
		c.cellularBaseMS, c.cellularVariationMS = median.Milliseconds(), sigma.Milliseconds()
		if mobile && c.rttEpoch == epoch {
			c.session.SetMobileBaseline(median, sigma)
		}
		c.mu.Unlock()
		desired := 0
		target := mail.AllocatedAccounts
		if mobile {
			// Wait for a complete window on a new bearer before reducing.
			if now.Sub(bearerSince) < cellularRTTWindow {
				samples = 0
			}
			desired = policy.decideObserved(target, average, samples, demand, now, evidence)
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
			bearerSince = time.Now()
			if mobile {
				policy.lastChange = bearerSince
				c.mu.Lock()
				if c.rttEpoch == epoch {
					c.rttHistory = nil
					c.rttSince = bearerSince
				}
				c.mu.Unlock()
			}
			c.RecordNetworkEvent("ACCOUNT_LIMIT_CHANGED", int64(target))
		}
	}
}
