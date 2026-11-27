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

const cellularRTTWindow = 5 * time.Second
const cellularEmergencyInterval = 3 * time.Second

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
	healthy    int
	limit      int
	lastChange time.Time
}

// Only decrease on a five-second rolling application RTT average, never
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
	active, streams, queued, slots, busy       int
	serviceCapacity, publications              float64
	bulkPressure, retries, timeout, fresh      bool
	stalled                                    bool // outstanding traffic without useful delivery
	queueBytes                                 int
	remoteDemand, remoteProgress, localHealthy bool
}

func (e cellularEvidence) downstreamProbe(average time.Duration) bool {
	return e.remoteDemand && e.remoteProgress && e.localHealthy && e.fresh && !e.timeout && !e.stalled && !e.bulkPressure && average > 0 && average < 1500*time.Millisecond
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

// Congestion takes precedence over mailbox saturation. A timeout or retry
// is not evidence that more simultaneous sockets would improve the bearer.
func (p *cellularPolicy) decideObserved(capacity int, average time.Duration, samples int, demand bool, now time.Time, e cellularEvidence) int {
	if capacity < servercfg.MinOperationalAccounts {
		return 0
	}
	if p.limit == 0 {
		p.limit = capacity
	}
	p.limit = min(capacity, max(servercfg.MinOperationalAccounts, p.limit))
	severe := (e.timeout || average >= 1500*time.Millisecond) && (e.stalled || e.bulkPressure || e.retries || e.queueBytes > 128<<10) && (demand || e.streams > 0 || e.queued > 0)
	congested := (e.timeout || samples >= 2 && average > 400*time.Millisecond) && (e.bulkPressure || e.stalled || e.retries && !e.downstreamProbe(average) || e.queueBytes > 128<<10)
	interval := cellularRTTWindow
	if severe {
		interval = cellularEmergencyInterval
	}
	if !p.lastChange.IsZero() && now.Sub(p.lastChange) < interval {
		return p.limit
	}
	old := p.limit
	if severe || congested {
		percent := 10
		if average > 600*time.Millisecond {
			percent = 20
		}
		if severe || average >= 700*time.Millisecond {
			percent = 35
		}
		p.limit = max(servercfg.MinOperationalAccounts, p.limit-accountStep(capacity, percent))
		p.healthy = 0
	} else if e.active < servercfg.MinOperationalAccounts {
		// The reconfiguration worker recovers the missing essential paths;
		// do not expand the target in response to a missing physical socket.
		p.healthy = 0
	} else if e.downstreamProbe(average) && samples >= 2 && p.limit < capacity {
		// Downstream backlog lives at the peer. Probe one extra account after
		// two fresh decisions when the local carrier is healthy, even if IMAP
		// delivery latency is naturally above the idle threshold.
		p.healthy++
		if p.healthy >= 2 {
			p.limit++
			p.healthy = 0
		}
	} else if !e.fresh || e.timeout || e.retries || e.stalled || samples < 2 || average > 400*time.Millisecond {
		p.healthy = 0
	} else {
		p.decide(capacity, average, samples, demand, now)
	}
	if old != p.limit {
		p.lastChange = now
		p.healthy = 0
	}
	return p.limit
}
func canLearnCellularBaseline(e cellularEvidence, rtt int64, demand bool, settled bool) bool {
	return settled && e.fresh && !e.timeout && !e.retries && !e.stalled && !e.overloaded() && !demand && e.streams == 0 && e.queued == 0 && rtt > 0 && rtt <= 400
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
	last := -1
	type result struct {
		desired int
		err     error
	}
	done := make(chan result, 1)
	var busy bool
	var cancelChange context.CancelFunc
	var nextAttempt time.Time
	var failures int
	var applied = -1
	var lastDelivered, lastPeerDelivered uint64
	var lastPeerProgress time.Time
	var lastProgress time.Time
	defer func() {
		if cancelChange != nil {
			cancelChange()
		}
		if busy {
			<-done
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case outcome := <-done:
			busy = false
			cancelChange = nil
			if outcome.desired != last {
				nextAttempt = time.Time{}
				continue
			}
			if outcome.err == nil {
				applied = outcome.desired
				failures = 0
				nextAttempt = time.Time{}
				c.RecordNetworkEvent("ACCOUNT_LIMIT_APPLIED", int64(outcome.desired))
			} else {
				failures++
				nextAttempt = time.Now().Add(time.Second * time.Duration(1<<min(failures, 4)))
				c.RecordNetworkEvent("ACCOUNT_LIMIT_RETRY", int64(outcome.desired))
			}
			continue
		case <-tick.C:
		}
		mail := c.link.Snapshot()
		if !mail.GrantNegotiated || mail.AllocatedAccounts < servercfg.MinOperationalAccounts {
			continue
		}
		now := time.Now()
		average, samples, currentEpoch, mobile := c.cellularRTT(now)
		if mobile != wasMobile || currentEpoch != epoch {
			policy = cellularPolicy{}
			last = -1
			applied = -1
			nextAttempt = time.Time{}
			lastProgress = now
			lastPressure = time.Time{}
			lastPeerProgress = time.Time{}
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
		var imapSamples []int64
		elapsed := now.Sub(sampled).Seconds()
		for _, line := range mail.Lines {
			old, ok := previous[line.Account]
			evidence.queueBytes += line.QueuedMIMEBytes
			if line.Allocated && line.Ready && line.Data && line.Writer && line.ControlWriter && !line.ScaleBlocked {
				active++
				if line.IMAPLatencyFresh && line.IMAPLatencyMillis > 0 {
					imapSamples = append(imapSamples, line.IMAPLatencyMillis)
				}
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
		} // never erase bearer RTT on socket churn

		// Physical byte counters include hedges/control; report effective
		// shares, never use them as independent capacity measurements.
		snapshot := c.session.Snapshot()
		if snapshot.PeerDemandFresh && snapshot.PeerBulkDelivered > lastPeerDelivered {
			lastPeerProgress = now
		}
		if snapshot.PeerDemandFresh {
			lastPeerDelivered = snapshot.PeerBulkDelivered
		}
		evidence.remoteDemand = snapshot.PeerDemandFresh && snapshot.PeerBulkPending >= 128<<10 && !snapshot.PeerDraining
		evidence.remoteProgress = !lastPeerProgress.IsZero() && now.Sub(lastPeerProgress) < 5*time.Second
		sort.Slice(imapSamples, func(i, j int) bool { return imapSamples[i] < imapSamples[j] })
		evidence.localHealthy = len(imapSamples) > 0 && imapSamples[len(imapSamples)/2] <= 150
		demand := evidence.remoteDemand || snapshot.PendingBytes >= 128<<10 || snapshot.DeliveryFlight >= 128<<10 || max(up, down) >= 1_000_000
		probe := c.Probe()
		evidence.active, evidence.streams = active, snapshot.Streams
		evidence.fresh = probe.Failure == "" && probe.SampledAtMS > 0 && now.UnixMilli()-probe.SampledAtMS < 3000
		evidence.timeout = probe.Failure != "" || (probe.SampledAtMS > 0 && now.UnixMilli()-probe.SampledAtMS > 6000)
		if snapshot.Retransmissions > previousRetries || snapshot.ReceiptBackpressure > previousBackpressure {
			lastPressure = now
		}
		previousRetries, previousBackpressure = snapshot.Retransmissions, snapshot.ReceiptBackpressure
		evidence.retries = !lastPressure.IsZero() && now.Sub(lastPressure) < cellularRTTWindow
		if lastProgress.IsZero() || snapshot.UniqueBulkDelivered != lastDelivered || snapshot.BulkDeliveryFlight == 0 && snapshot.PendingBytes < 128<<10 {
			lastProgress = now
		}
		lastDelivered = snapshot.UniqueBulkDelivered
		evidence.stalled = (snapshot.BulkDeliveryFlight > 0 || snapshot.PendingBytes >= 128<<10) && now.Sub(lastProgress) >= 3*time.Second
		evidence.bulkPressure = snapshot.BulkDeliveryFlight >= 128<<10 || snapshot.PendingBytes >= 128<<10 || evidence.queueBytes >= 128<<10
		settled := now.Sub(bearerSince) >= cellularRTTWindow && !mail.GrantChanging && !busy
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
		congested := mobile && (evidence.timeout || evidence.stalled || (evidence.retries || average > 400*time.Millisecond) && !evidence.downstreamProbe(average))
		c.session.SetMobileRecovery(congested, desired)
		if desired != last {
			last = desired
			nextAttempt = time.Time{}
			failures = 0
			if cancelChange != nil {
				cancelChange()
			}
			c.RecordNetworkEvent("ACCOUNT_LIMIT_CHANGED", int64(target))
		}
		if busy || now.Before(nextAttempt) || mail.GrantChanging {
			continue
		}
		// Do not repeatedly revive optional accounts simply because their physical
		// socket count is temporarily below target. Grant health owns replacements.
		if applied == desired && (active == target || congested && active < target && active >= servercfg.MinOperationalAccounts) {
			continue
		}
		if congested && active >= servercfg.MinOperationalAccounts && active < target {
			continue
		}
		change, cancel := context.WithTimeout(ctx, 15*time.Second)
		cancelChange = cancel
		busy = true
		go func(wanted int) { defer cancel(); done <- result{wanted, c.session.SetActiveLimit(change, wanted)} }(desired)
	}
}
