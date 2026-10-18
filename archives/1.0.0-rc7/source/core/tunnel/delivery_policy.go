package tunnel

import "time"

// The modes describe useful delivery, not carrier connectivity. Outage never
// refunds an unknown physical APPEND or unacknowledged logical byte range.
type deliveryMode string

const (
	deliveryIdle      deliveryMode = "IDLE"
	deliveryNormal    deliveryMode = "NORMAL"
	deliveryProbeUp   deliveryMode = "PROBE_UP"
	deliveryCongested deliveryMode = "CONGESTED"
	deliveryOutage    deliveryMode = "OUTAGE"
	deliveryRecovery  deliveryMode = "RECOVERY"
)

type deliveryFeedback struct {
	Window, Flight, ACKFlight, Delivered, InitialWindow uint64
	Demand                                              bool
	RateMbps                                            float64
	Age, FastAge, Target, FastTarget, FastBaseline      time.Duration
	Samples                                             int
	Pressure                                            bool
}

type deliveryPolicy struct {
	mode                                   deliveryMode
	capacityMbps                           float64
	lastProgress, lastProbe, lastReduction time.Time
	delivered                              uint64
	healthy                                int
	probes, accepted, rejected             uint64
}

func (p *deliveryPolicy) next(now time.Time, f deliveryFeedback) uint64 {
	window := f.Window
	fastTarget := f.FastTarget
	if fastTarget <= 0 {
		fastTarget = deliveryFastMaxAge
	}
	// Apply early queue headroom above the unloaded path, not to total RTT.
	// A high propagation/jitter baseline must never lie above its own guard.
	fastEarly := fastTarget * 7 / 10
	if f.FastBaseline > 0 {
		baseline := min(f.FastBaseline, fastTarget)
		fastEarly = baseline + (fastTarget-baseline)*7/10
	}
	if p.mode == "" {
		p.mode = deliveryNormal
	}
	progress := f.Delivered > p.delivered
	if p.lastProgress.IsZero() {
		p.lastProgress = now
		if !progress && f.Age > 0 {
			p.lastProgress = now.Add(-f.Age)
		}
	} else if progress {
		p.lastProgress = now
	}
	p.delivered = f.Delivered
	if !f.Demand && f.Flight == 0 {
		if p.mode != deliveryIdle {
			p.lastProbe = time.Time{}
			p.lastReduction = time.Time{}
			p.lastProgress = time.Time{}
		}
		p.mode = deliveryIdle
		p.healthy = 0
		// A download/upload phase change or the next speed test starts a fresh
		// bulk epoch. Reusing a saturated 16 MiB flight can fill both directions'
		// mail journals before the new path has delivered its first useful ACK.
		// This only bounds new admission after all logical bulk has drained.
		p.capacityMbps = 0
		// A previous narrow congestion window is not the next test's capacity.
		// Start a drained epoch from its independently bounded cold-path probe.
		seed := f.InitialWindow
		if seed == 0 {
			seed = deliveryInitialWindow
		}
		return min(seed, uint64(deliveryInitialWindow))
	}
	if f.Flight > 0 && f.Age >= 3*time.Second && now.Sub(p.lastProgress) >= 3*time.Second {
		p.mode = deliveryOutage
		p.healthy = 0
		return max(uint64(deliveryMinWindow), window/2)
	}
	if f.Flight > 0 && f.Delivered == 0 && f.Age >= max(2*time.Second, f.Target) {
		// An initial flight with no useful ACK is not a measured slow path.
		// Stop adding bulk promptly while keeping every charged range intact.
		p.mode = deliveryCongested
		p.healthy = 0
		p.lastReduction, p.lastProbe = now, now
		return deliveryMinWindow
	}
	// Mail delivery ACKs arrive in batches, often farther apart than an actor
	// tick. Silence in one 250 ms sample is not evidence of a capacity drop.
	// Outstanding short-queue pressure still reacts immediately; absent that,
	// require an entire feedback turn without authenticated useful progress.
	stalled := now.Sub(p.lastProgress) >= max(time.Second, f.Target)
	// A replicated BODY can hold a TCP frontier while independent streams and
	// short traffic remain healthy. Fresh useful ACKs are not themselves queue
	// pressure. Reduce on short-path delay or a full turn without progress;
	// the collective outage guard above remains independent of this decision.
	if f.Pressure || f.FastAge > fastTarget || f.Age > f.Target && (f.FastAge > fastEarly || stalled) {
		if p.mode == deliveryProbeUp {
			p.rejected++
		}
		p.mode = deliveryCongested
		p.healthy = 0
		// One ACK/path-delay episode must not halve the session every actor
		// tick. Give the previous reduction a complete feedback turn before
		// evaluating its effect; persistent stalls still reduce to the floor.
		turn := max(time.Second, f.Target)
		if !p.lastReduction.IsZero() && now.Sub(p.lastReduction) < turn {
			return window
		}
		p.lastReduction = now
		p.lastProbe = now
		reduced := max(uint64(deliveryMinWindow), window/2)
		if progress && f.RateMbps > 0 && f.RateMbps < 10000 {
			// On a capacity drop, one halving may still leave seconds of old
			// queue. Size new flight from authenticated useful-byte delivery,
			// never from physical APPEND throughput or a carrier-count multiplier.
			flight := uint64(f.RateMbps * 1e6 / 8 * f.Target.Seconds())
			reduced = min(reduced, max(uint64(deliveryMinWindow), flight))
		}
		return reduced
	}
	if !f.Demand {
		// Draining existing work is not a request for extra publication credit.
		return window
	}
	if p.mode == deliveryIdle {
		p.mode = deliveryNormal
	}
	// Use delivered application bytes. An application-limited/pacing-limited
	// low sample cannot ratchet the estimate down permanently.
	if f.RateMbps > 0 && (progress || f.Samples > 0) {
		if p.capacityMbps == 0 || f.RateMbps > p.capacityMbps {
			p.capacityMbps = f.RateMbps
		} else if max(f.Flight, f.ACKFlight) >= window/2 {
			p.capacityMbps = .875*p.capacityMbps + .125*f.RateMbps
		}
	}

	if p.mode == deliveryOutage || p.mode == deliveryCongested {
		p.mode = deliveryRecovery
	}
	if f.Samples == 0 && !progress {
		return window
	}
	p.healthy++
	// Only fresh useful ACKs shorten the discovery interval. Empty actor ticks
	// cannot grow flight; sustained congestion retains its full reduction turn.
	probeTurn := time.Second
	if progress && f.Samples > 0 && f.Age < f.Target-100*time.Millisecond {
		probeTurn = max(deliveryDecisionEpoch, min(time.Second, f.Age))
	}
	if !p.lastProbe.IsZero() && now.Sub(p.lastProbe) < probeTurn {
		return window
	}
	if p.healthy < 2 && p.mode == deliveryRecovery {
		return window
	}
	// Bounded probing is independent of a self-imposed pacing ceiling. A BDP
	// estimate is a starting point, never an excuse to exceed the latency guard.
	step := uint64(64 << 10)
	if progress && f.Samples > 0 && max(f.Flight, f.ACKFlight) >= window/2 && f.Age < f.Target-100*time.Millisecond {
		// A small starting flight can itself keep measured rate below 8 Mbps.
		// Fresh ACKs at full utilization and below the delay guard authorize a
		// bounded probe; requiring that rate first creates a circular slow ramp.
		step = max(step, window/2)
	}
	// RC5: close a short-test cold-start feedback loop. A *fresh authenticated*
	// useful-byte ACK under half-window-or-higher demand and comfortably below
	// the bulk age budget can grow a small, fully utilized flight by one window
	// rather than by half. This does not authorize physical journal spending,
	// replay, or additional sockets. Stop accelerating once flight reaches 4 MiB;
	// the established controller/RTT guard owns all subsequent probing.
	if progress && f.Samples > 0 && window <= 4<<20 &&
		f.Flight >= window/2 && f.Age > 0 &&
		f.Age < f.Target*3/4 && f.FastAge < fastEarly && !f.Pressure {
		step = max(step, window)
	}
	if p.capacityMbps >= 8 && f.Age < f.Target-100*time.Millisecond {
		bdp := uint64(p.capacityMbps * 1e6 / 8 * max(200*time.Millisecond, min(f.Age, f.Target)).Seconds())
		step = max(step, window/4)
		if bdp > window {
			step = min(window/2, max(step, bdp-window))
		}
	}
	if f.Age >= f.Target-100*time.Millisecond {
		// Plateau probes require fresh useful progress and hysteresis.
		if !progress || p.healthy < 3 {
			return window
		}
		step = 64 << 10
	}
	if p.mode == deliveryProbeUp {
		p.accepted++
	}
	p.mode = deliveryProbeUp
	p.probes++
	p.lastProbe = now
	if window >= deliveryMaxWindow {
		p.mode = deliveryNormal
	}
	return min(uint64(deliveryMaxWindow), window+step)
}
