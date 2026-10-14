package tunnel

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/flow"
)

// One session-wide logical delivery budget. Physical journal debt is separate
// and is released only by the existing observed-body/deletion proof contract.
// There is no token bucket, future rate appointment, floor bypass or second
// download window. A reservation precedes grant selection and journal Prepare.
const (
	deliveryInitialWindow = 1 << 20
	deliveryMinWindow     = 64 << 10
	deliveryMaxWindow     = 16 << 20
	deliveryFastReserve   = 128 << 10
	deliveryTargetAge     = 800 * time.Millisecond
	deliveryDecisionEpoch = 250 * time.Millisecond
	// Short DATA ACK is one leg of an application exchange; keep headroom for
	// its other leg. This is feedback, not the 1500ms application test limit.
	deliveryFastMaxAge = 650 * time.Millisecond
	deliveryFastHold   = 2 * time.Second
	deliveryMaxRanges  = 2048
	deliveryMaxWaiters = 128
)

var errDeliveryLimit = errors.New("DELIVERY_FLIGHT_LIMIT")
var errDeliveryStopped = errors.New("DELIVERY_STREAM_STOPPED")
var errDeliveryDeferred = errors.New("DELIVERY_MEMBER_DEFERRED")

type deliveryDeferredError struct{ wake <-chan struct{} }

func (e *deliveryDeferredError) Error() string { return errDeliveryDeferred.Error() }
func (e *deliveryDeferredError) Unwrap() error { return errDeliveryDeferred }

type deliveryKey struct{ stream, offset uint64 }
type deliveryRange struct {
	key               deliveryKey
	start, end        uint64
	fast              bool
	admitted          time.Time
	accepted          time.Time // last definitively accepted physical publication
	flightAtAdmission uint64
	unloadedFast      bool   // original short DATA admitted outside a bulk epoch
	isolatedFast      bool   // short-only startup traffic is not bulk queue evidence
	carrier           string // advisory physical placement until logical ACK
}
type deliveryWaiter struct {
	fast, repair bool
	jobs         []*datapath.Job
}
type deliveryController struct {
	wake                                chan struct{}
	window                              uint64
	initialWindow                       uint64
	ranges                              map[deliveryKey]*deliveryRange
	frontiers                           map[uint64]uint64
	issued                              map[uint64]uint64
	stopped                             map[uint64]bool
	waiters                             []*deliveryWaiter
	repair                              bool
	bulk, fast                          uint64
	ackFlight                           uint64 // peak bulk flight at a fresh authenticated ACK, one decision only
	fastCarrierRanges                   map[string]int
	carrierBytes                        map[string]uint64
	carrierACK                          map[string]time.Duration
	carrierACKAt                        map[string]time.Time
	baselineMS                          int64
	observedAgeMS                       int64
	ages                                [64]time.Duration
	ageCount, ageNext                   int
	lastDecision                        time.Time
	nextRepairWake                      time.Time
	bulkDemandAt                        time.Time
	fastPressureUntil                   time.Time
	lastFastACKAgeMS                    int64
	lastFastACKAt                       time.Time
	policy                              deliveryPolicy
	fastPressureDecisions               uint64
	decisions                           uint64
	lowFlightAges                       [8]time.Duration
	lowFlightCount, lowFlightNext       int
	fastBaselineAges                    [16]time.Duration
	fastBaselineCount, fastBaselineNext int
	lastRateAt                          time.Time
	lastDelivered                       uint64
	delivered                           uint64
	bulkDelivered                       uint64
	lastBulkDelivered                   uint64
	rateMbps                            float64
	bulkRateMbps                        float64
}

func newDeliveryController() deliveryController {
	return deliveryController{wake: make(chan struct{}), window: deliveryInitialWindow, initialWindow: deliveryInitialWindow, ranges: map[deliveryKey]*deliveryRange{}, frontiers: map[uint64]uint64{}, issued: map[uint64]uint64{}, stopped: map[uint64]bool{}}
}
func (d *deliveryController) init() {
	if d.window == 0 {
		*d = newDeliveryController()
	}
	if d.wake == nil {
		d.wake = make(chan struct{})
	}
}

// Caller holds the group mutex. Each waiter observes the exact generation of
// the predicate it checked; closing it broadcasts without lost notifications.
func (d *deliveryController) changed() {
	d.init()
	close(d.wake)
	d.wake = make(chan struct{})
}
func (d *deliveryController) remove(r *deliveryRange) {
	if d.ranges[r.key] != r {
		return
	}
	n := r.end - r.start
	if r.fast {
		d.fast -= n
	} else {
		d.bulk -= n
	}
	d.bindCarrier(r, "")
	delete(d.ranges, r.key)
}

// Placement cost is advisory. Only authenticated logical ACK/stream retirement
// ends this range; it never returns journal quota or proves physical cleanup.
func (d *deliveryController) bindCarrier(r *deliveryRange, carrier string) {
	if r.carrier == carrier {
		return
	}
	if d.carrierBytes == nil {
		d.carrierBytes = map[string]uint64{}
	}
	if d.fastCarrierRanges == nil {
		d.fastCarrierRanges = map[string]int{}
	}
	if r.carrier != "" {
		d.removeCarrierBytes(r.carrier, r.end-r.start)
		if r.fast {
			d.fastCarrierRanges[r.carrier]--
			if d.fastCarrierRanges[r.carrier] == 0 {
				delete(d.fastCarrierRanges, r.carrier)
			}
		}
	}
	r.carrier = carrier
	if carrier != "" {
		d.carrierBytes[carrier] += r.end - r.start
		if r.fast {
			d.fastCarrierRanges[carrier]++
		}
	}
}

func (d *deliveryController) removeCarrierBytes(carrier string, n uint64) {
	if carrier == "" || n == 0 {
		return
	}
	d.carrierBytes[carrier] -= n
	if d.carrierBytes[carrier] == 0 {
		delete(d.carrierBytes, carrier)
	}
}

func (g *stripeGroup) bindDeliveryCarrier(jobs []*datapath.Job, carrier string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, j := range jobs {
		a := j.Attempt()
		for _, r := range g.delivery.ranges {
			if r.key.stream == a.Stream && a.Offset < r.end && a.Offset+a.PayloadBytes > r.start {
				g.delivery.bindCarrier(r, carrier)
				if r.fast && g.owners[a.Stream] != nil {
					if g.shortAffinity == nil {
						g.shortAffinity = map[uint64]string{}
					}
					g.shortAffinity[a.Stream] = carrier
				}
			}
		}
	}
}
func (d *deliveryController) progress(stream, frontier uint64, now time.Time) {
	d.init()
	if frontier <= d.frontiers[stream] {
		return
	}
	d.frontiers[stream] = frontier
	for key, r := range d.ranges {
		if key.stream != stream || frontier <= r.start {
			continue
		}
		n := min(frontier, r.end) - r.start
		d.removeCarrierBytes(r.carrier, n)
		d.delivered += n
		if r.fast {
			d.fast -= n
		} else {
			d.ackFlight = max(d.ackFlight, d.bulk)
			d.bulk -= n
			d.bulkDelivered += n
		}
		r.start += n
		if r.start == r.end {
			residence := max(time.Duration(0), now.Sub(r.admitted))
			// Only signed logical ACKs create carrier samples. Repair and unknown
			// attempts and retirement neither prove delivery nor refund journal debt.
			if r.carrier != "" && !r.accepted.IsZero() && r.flightAtAdmission > 0 {
				age := max(time.Millisecond, now.Sub(r.accepted))
				if d.carrierACK == nil {
					d.carrierACK = map[string]time.Duration{}
					d.carrierACKAt = map[string]time.Time{}
				}
				d.carrierACKAt[r.carrier] = now
				old := d.carrierACK[r.carrier]
				if old == 0 {
					d.carrierACK[r.carrier] = age
				} else {
					d.carrierACK[r.carrier] = (7*old + age) / 8
				}
			}
			if r.fast {
				d.lastFastACKAgeMS = residence.Milliseconds()
				d.lastFastACKAt = now
				if r.unloadedFast {
					// Measure the complete authenticated DATA/ACK path, including
					// mail command legs and normal radio jitter, without bulk queue.
					if residence > 0 && residence <= 2*time.Second {
						d.fastBaselineAges[d.fastBaselineNext] = residence
						d.fastBaselineNext = (d.fastBaselineNext + 1) % len(d.fastBaselineAges)
						d.fastBaselineCount = min(d.fastBaselineCount+1, len(d.fastBaselineAges))
					}
				} else if !r.isolatedFast {
					if residence > d.fastAgeBudget() {
						d.fastPressureUntil = now.Add(deliveryFastHold)
					} else {
						// Only fresh bulk-exposed ACKs supersede a slow queue sample.
						d.fastPressureUntil = time.Time{}
					}
				}
			} else {
				d.ages[d.ageNext] = residence
				// Sample only a newly charged original at low aggregate flight.
				// Repair ACKs and stalled full-window traffic cannot raise the floor.
				if r.flightAtAdmission > 0 && r.flightAtAdmission <= deliveryInitialWindow/2 && residence > 0 {
					d.lowFlightAges[d.lowFlightNext] = residence
					d.lowFlightNext = (d.lowFlightNext + 1) % len(d.lowFlightAges)
					d.lowFlightCount = min(d.lowFlightCount+1, len(d.lowFlightAges))
				}
				d.ageNext = (d.ageNext + 1) % len(d.ages)
				d.ageCount = min(d.ageCount+1, len(d.ages))
			}
			d.bindCarrier(r, "")
			delete(d.ranges, key)
		}
	}
	d.changed()
}
func (d *deliveryController) retire(stream uint64) {
	d.init()
	if d.stopped[stream] {
		return
	}
	d.stopped[stream] = true
	for key, r := range d.ranges {
		if key.stream == stream {
			d.remove(r)
		}
	}
	d.changed()
	// Stream IDs are never reused within the authenticated logical generation;
	// ITH9 limits it to 4,096 IDs, bounding frontier/issued/stop metadata.
}

// One feedback policy owns admission. Physical journal debt is never changed
// by a rate decision. Demand-limited samples cannot lower the capacity estimate.
func (d *deliveryController) maintain(now time.Time, demand bool) {
	d.init()
	if !d.nextRepairWake.IsZero() && !now.Before(d.nextRepairWake) {
		d.nextRepairWake = time.Time{}
		d.changed()
	}
	if demand {
		d.bulkDemandAt = now
	}
	if d.lastRateAt.IsZero() {
		d.lastRateAt = now
		d.lastDelivered = d.delivered
		d.lastBulkDelivered = d.bulkDelivered
	}
	// A full DATA/ACK turn, rather than the actor tick, measures useful rate.
	// Otherwise a large batch followed by an empty tick sizes the next BDP from
	// zero or a small tail fragment and repeatedly collapses the flight budget.
	if elapsed := now.Sub(d.lastRateAt); elapsed >= max(time.Second, d.targetAge()) {
		d.rateMbps = float64(d.delivered-d.lastDelivered) * 8 / elapsed.Seconds() / 1e6
		d.bulkRateMbps = float64(d.bulkDelivered-d.lastBulkDelivered) * 8 / elapsed.Seconds() / 1e6
		d.lastRateAt = now
		d.lastDelivered = d.delivered
		d.lastBulkDelivered = d.bulkDelivered
	}
	if !d.lastDecision.IsZero() && now.Sub(d.lastDecision) < deliveryDecisionEpoch {
		return
	}
	ageDemand := now.Sub(d.bulkDemandAt)
	demand = demand || (!d.bulkDemandAt.IsZero() && ageDemand >= 0 && ageDemand <= time.Second)
	if !demand {
		d.bulkDemandAt = time.Time{}
	}
	var age, fastAge time.Duration
	for _, r := range d.ranges {
		residence := max(time.Duration(0), now.Sub(r.admitted))
		if r.fast && !r.isolatedFast {
			fastAge = max(fastAge, residence)
		} else if !r.fast {
			age = max(age, residence)
		}
	}
	if d.ageCount > 0 {
		values := append([]time.Duration(nil), d.ages[:d.ageCount]...)
		sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
		age = max(age, values[(len(values)-1)*9/10])
	}
	if fastAge > d.fastAgeBudget() {
		d.fastPressureUntil = now.Add(deliveryFastHold)
	}
	d.lastDecision = now
	d.decisions++
	d.observedAgeMS = age.Milliseconds()
	if !demand && d.bulk == 0 && d.fast == 0 {
		// A completely drained epoch cannot retain a prior congestion hold.
		// Flight and original physical quota debt are unaffected.
		d.fastPressureUntil = time.Time{}
	}
	before := d.window
	d.window = d.policy.next(now, deliveryFeedback{Window: d.window, InitialWindow: d.initialWindow, Flight: d.bulk, ACKFlight: d.ackFlight, Demand: demand,
		Delivered: d.bulkDelivered, RateMbps: d.bulkRateMbps, Age: age, FastAge: fastAge,
		Samples: d.ageCount, Target: d.targetAge(), FastTarget: d.fastAgeBudget(), FastBaseline: d.fastPathBaseline(), Pressure: now.Before(d.fastPressureUntil)})
	if d.policy.mode == deliveryCongested || d.policy.mode == deliveryOutage {
		d.fastPressureDecisions++
	}
	if before != d.window {
		d.changed()
	}
	d.ackFlight = 0
	d.ageCount = 0
	d.ageNext = 0
}

// A DATA delivery ACK traverses more mail commands than the provider probe.
// Learn its lower-quartile floor from bounded low-flight original samples.
// Fixed caps prevent a stalled provider from legitimizing unbounded queue age.
func (d *deliveryController) acknowledgementFloor() time.Duration {
	if d.lowFlightCount < 4 {
		return 0
	}
	values := append([]time.Duration(nil), d.lowFlightAges[:d.lowFlightCount]...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return min(values[(len(values)-1)/4], 2*time.Second)
}
func (d *deliveryController) invalidateRepairFloor(stream, start, end uint64) {
	for _, r := range d.ranges {
		if r.key.stream == stream && start < r.end && end > r.start {
			r.flightAtAdmission = 0
			r.unloadedFast = false
		}
	}
}

func (d *deliveryController) targetAge() time.Duration {
	// The objective is fixed. Calibration describes propagation; it cannot
	// legitimize a loaded queue above the application's one-second RTT goal.
	return deliveryTargetAge
}

type deliveryReservation struct {
	g        *stripeGroup
	inserted []*deliveryRange
	jobs     []*datapath.Job
	repair   bool
	once     sync.Once
}

func (r *deliveryReservation) finish(jobs []*datapath.Job, results []datapath.Completion) {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.g.mu.Lock()
		defer r.g.mu.Unlock()
		d := &r.g.delivery
		changed := r.repair
		if r.repair {
			d.repair = false
		}
		retained := map[deliveryKey]bool{}
		for i, j := range jobs {
			if i < len(results) && results[i].Outcome == flow.NotCommitted {
				a := j.Attempt()
				for _, r := range d.ranges {
					if r.key.stream == a.Stream && a.Offset < r.end && a.Offset+a.PayloadBytes > r.start {
						d.bindCarrier(r, "")
					}
				}
			}
			if i < len(results) && (results[i].Outcome == flow.Committed || results[i].Outcome == flow.CommitUnknown || results[i].Outcome == flow.NotCommitted && results[i].NonFatal) {
				a := j.Attempt()
				retained[deliveryKey{a.Stream, a.Offset}] = true
			}
			if i < len(results) && (results[i].Outcome == flow.Committed || results[i].Outcome == flow.CommitUnknown) {
				a := j.Attempt()
				for _, entry := range d.ranges {
					if entry.key.stream == a.Stream && a.Offset < entry.end && a.Offset+a.PayloadBytes > entry.start {
						if results[i].Outcome == flow.Committed {
							entry.accepted = time.Now()
						} else {
							entry.accepted = time.Time{}
						}
					}
				}
			}
		}
		for _, entry := range r.inserted {
			if !retained[entry.key] {
				d.remove(entry)
				changed = true
			}
		}
		if changed {
			d.changed()
		}
		r.g.signal()
	})
}

// FIFO within each work class; a full bulk budget cannot block short DATA or
// the single authenticated repair slot. Network and durable IO happen later.
func (g *stripeGroup) reserveDelivery(ctx context.Context, jobs []*datapath.Job) (*deliveryReservation, error) {
	if g == nil || len(jobs) == 0 {
		return nil, errDeliveryLimit
	}
	w := &deliveryWaiter{fast: true, jobs: jobs}
	for _, j := range jobs {
		if j == nil {
			return nil, errDeliveryLimit
		}
		w.fast = w.fast && j.Interactive()
		w.repair = w.repair || j.Attempt().Repair
	}
	g.mu.Lock()
	g.delivery.init()
	if len(g.delivery.waiters) >= deliveryMaxWaiters {
		g.mu.Unlock()
		return nil, errDeliveryLimit
	}
	g.delivery.waiters = append(g.delivery.waiters, w)
	g.mu.Unlock()
	defer func() {
		g.mu.Lock()
		for i, p := range g.delivery.waiters {
			if p == w {
				g.delivery.waiters = append(g.delivery.waiters[:i], g.delivery.waiters[i+1:]...)
				break
			}
		}
		g.signal()
		g.mu.Unlock()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g.mu.Lock()
		d := &g.delivery
		d.maintain(time.Now(), !w.fast && !w.repair)
		entries, newBytes, selected, planErr := d.plan(w)
		if planErr != nil {
			if errors.Is(planErr, errDeliveryDeferred) {
				planErr = &deliveryDeferredError{wake: d.wake}
			}
			g.mu.Unlock()
			return nil, planErr
		}
		first := true
		for _, p := range d.waiters {
			if p == w {
				break
			}
			if p.fast == w.fast && p.repair == w.repair {
				_, _, candidates, e := d.plan(p)
				// A worker can arrive before the collector's preceding batch.
				// Waiting future offsets must never block the missing predecessor.
				if e == nil && len(candidates) > 0 {
					first = false
					break
				}
			}
		}
		if len(d.ranges)+len(entries) > deliveryMaxRanges {
			g.mu.Unlock()
			return nil, errDeliveryLimit
		}
		admit := first && len(selected) > 0 && (!w.repair || !d.repair)
		if newBytes == 0 {
			// Already charged delivery may need authenticated repair after a window
			// decrease. A smaller new-admission budget cannot prevent its own ACK.
		} else if w.fast {
			admit = admit && d.fast+newBytes <= deliveryFastReserve
		} else {
			admit = admit && d.bulk+newBytes <= d.window
		}
		if admit {
			if newBytes > 0 {
				// Concurrent short startups cannot calibrate an unloaded path.
				// Actual bulk exposure additionally makes their age queue evidence.
				for _, pending := range d.ranges {
					pending.unloadedFast = false
					// Bulk arriving later cannot have caused an earlier short
					// exchange's existing delay. Keep its admission-time exposure.
					// New short traffic admitted during bulk remains exposed.
				}
			}
			for _, r := range entries {
				r.isolatedFast = w.fast && !w.repair && d.bulk == 0
				r.unloadedFast = r.isolatedFast && d.fast == 0 && newBytes <= 8<<10 && len(entries) == 1
				if !w.fast && !w.repair {
					r.flightAtAdmission = d.bulk + newBytes
				}
				d.ranges[r.key] = r
				d.issued[r.key.stream] = max(d.issued[r.key.stream], r.end)
			}
			if w.fast {
				d.fast += newBytes
			} else {
				d.bulk += newBytes
			}
			if w.repair {
				for _, job := range selected {
					a := job.Attempt()
					d.invalidateRepairFloor(a.Stream, a.Offset, a.Offset+a.PayloadBytes)
				}
				d.repair = true
			}
			if len(entries) > 0 || w.repair {
				d.changed()
			}
			g.mu.Unlock()
			return &deliveryReservation{g: g, inserted: entries, jobs: selected, repair: w.repair}, nil
		}
		wake := d.wake
		g.mu.Unlock()
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// A partial batch can finish after an early ACK. Recheck under the same mutex
// before parking omitted members, so already available credit cannot lose a wake.
func (g *stripeGroup) deliveryRetryWake(j *datapath.Job) <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	d := &g.delivery
	d.init()
	_, _, selected, err := d.plan(&deliveryWaiter{fast: j.Interactive(), repair: j.Attempt().Repair, jobs: []*datapath.Job{j}})
	if len(selected) > 0 || errors.Is(err, errDeliveryStopped) {
		ready := make(chan struct{})
		close(ready)
		return ready
	}
	return d.wake
}

// Admission order follows logical offsets, independent of the order physical
// worker goroutines reach this controller. Filling a bounded window with later
// offsets while the original predecessor waits would deadlock its own ACK.
func (d *deliveryController) plan(w *deliveryWaiter) ([]*deliveryRange, uint64, []*datapath.Job, error) {
	seen := map[deliveryKey]bool{}
	next := map[uint64]uint64{}
	jobs := append([]*datapath.Job(nil), w.jobs...)
	sort.Slice(jobs, func(i, j int) bool {
		a, b := jobs[i].Attempt(), jobs[j].Attempt()
		if a.Stream != b.Stream {
			return a.Stream < b.Stream
		}
		return a.Offset < b.Offset
	})
	var entries []*deliveryRange
	var selected []*datapath.Job
	var size uint64
	orderBlocked := false
	remaining := uint64(0)
	if w.fast {
		if d.fast < deliveryFastReserve {
			remaining = deliveryFastReserve - d.fast
		}
	} else if d.bulk < d.window {
		remaining = d.window - d.bulk
	}
	for _, j := range jobs {
		a := j.Attempt()
		key := deliveryKey{a.Stream, a.Offset}
		if seen[key] || a.Offset+a.PayloadBytes < a.Offset {
			return nil, 0, nil, errStripeStream
		}
		seen[key] = true
		if d.stopped[a.Stream] {
			return nil, 0, nil, errDeliveryStopped
		}
		if a.Repair && d.deferAcceptedRepair(a.Stream, a.Offset, a.Offset+a.PayloadBytes, time.Now()) {
			orderBlocked = true
			continue
		}
		if a.Repair || d.ranges[key] != nil || a.Offset+a.PayloadBytes <= d.frontiers[a.Stream] {
			selected = append(selected, j)
			continue
		}
		expected, ok := next[a.Stream]
		if !ok {
			expected = max(d.issued[a.Stream], d.frontiers[a.Stream])
		}
		start := max(a.Offset, d.frontiers[a.Stream])
		n := a.Offset + a.PayloadBytes - start
		// Do not hold an entire mixed-stream batch behind one future offset or
		// more bytes than the remaining window. Publish ready prefixes; unselected
		// members yield before sealing/Prepare and return to the logical actor.
		if a.Offset > expected {
			orderBlocked = true
			continue
		}
		if n > remaining || len(d.ranges)+len(entries) >= deliveryMaxRanges {
			continue
		}
		next[a.Stream] = max(expected, a.Offset+a.PayloadBytes)
		remaining -= n
		size += n
		entries = append(entries, &deliveryRange{key: key, start: start, end: a.Offset + a.PayloadBytes, fast: w.fast, admitted: time.Now()})
		selected = append(selected, j)
	}
	if len(selected) == 0 && orderBlocked {
		return nil, 0, nil, errDeliveryDeferred
	}
	return entries, size, selected, nil
}

func (g *stripeGroup) deliveryProgress(stream, frontier uint64) {
	g.mu.Lock()
	g.delivery.progress(stream, frontier, time.Now())
	g.signal()
	g.mu.Unlock()
}
func (g *stripeGroup) deliveryStopped(stream uint64) {
	g.mu.Lock()
	g.delivery.retire(stream)
	g.signal()
	g.mu.Unlock()
}
func (g *stripeGroup) maintainDelivery(now time.Time) {
	if g == nil {
		return
	}
	g.mu.Lock()
	before := g.delivery.window
	bulkDemand := false
	for _, waiter := range g.delivery.waiters {
		if !waiter.fast && !waiter.repair {
			bulkDemand = true
			break
		}
	}
	g.delivery.maintain(now, bulkDemand)
	if before != g.delivery.window {
		g.signal()
	}
	g.mu.Unlock()
}
func (g *stripeGroup) observeProviderBaseline(rtt time.Duration) {
	if g == nil || rtt <= 0 {
		return
	}
	g.mu.Lock()
	g.delivery.init()
	d := &g.delivery
	sample := max(int64(1), rtt.Milliseconds())
	cold := g.active.Load() == 0 && len(d.ranges) == 0 && d.delivered == 0
	if d.baselineMS == 0 || cold && sample < d.baselineMS {
		d.baselineMS = sample
		if cold {
			// A startup echo can include another account's boot queue. Before
			// application work begins, retain the lowest independent cold
			// observation instead of freezing whichever account arrived first.
			seed := uint64(deliveryInitialWindow)
			if sample >= 250 {
				seed = 256 << 10
			}
			d.window = seed
			d.initialWindow = seed
		}
	}
	g.mu.Unlock()
}

// NACK proves a receiver hole, not physical loss: STATUS can overtake a BODY
// already accepted by IMAP. Delay copies of that exact accepted range by the
// observed session delivery age. Unknown outcomes remain immediately eligible.
// One bounded wake is owned by the existing actor tick; no per-job timers.
func (d *deliveryController) deferAcceptedRepair(stream, start, end uint64, now time.Time) bool {
	grace := max(2*time.Second, min(10*time.Second, time.Duration(d.observedAgeMS)*time.Millisecond+2*time.Duration(d.baselineMS)*time.Millisecond))
	deferred := false
	for _, r := range d.ranges {
		if r.key.stream != stream || start >= r.end || end <= r.start || r.accepted.IsZero() {
			continue
		}
		until := r.accepted.Add(grace)
		if now.Before(until) {
			deferred = true
			if d.nextRepairWake.IsZero() || until.Before(d.nextRepairWake) {
				d.nextRepairWake = until
			}
		}
	}
	return deferred
}

// Preserve the diagnostic DTO name for existing Android readers. Removed
// controller fields stay zero; Mode and flight counters describe the new path.
type latencyGovernorSnapshot struct {
	Mode                                                                                       string
	BaselineMS, TargetMS, ObservedRTTMS                                                        int64
	AcknowledgementFloorMS                                                                     int64
	LastFastACKAgeMS, FastPressureRemainingMS, FastPathBaselineMS, FastTargetMS                int64
	FastPressureDecisions                                                                      uint64
	ProbeAttempts, ProbeAccepted, ProbeRejected                                                uint64
	RecoveryAttempts, RecoveryAccepted, RecoveryRejected, FastRampIncreases, FastRampRollbacks uint64
	FastRampActive                                                                             bool
	ProbeActive                                                                                bool
	BulkLimit, BulkInFlight, FeedbackSamples                                                   int
	MaxCarrierRTTMS                                                                            int64
	FloorBypass                                                                                bool
	RateMbps, BulkRateMbps, RateEWMA, RateLimitMbps                                            float64
	DecisionCount, EmergencyDecisionCount                                                      uint64
	DownloadWindowBytes, DownloadBacklogBytes, DownloadPendingBytes                            uint64
	FastFlightBytes                                                                            uint64
	FlightRanges, AdmissionWaiters                                                             int
	PendingShortRangesByAccount                                                                map[string]int
}

func (g *stripeGroup) deliverySnapshot() latencyGovernorSnapshot {
	if g == nil {
		return latencyGovernorSnapshot{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliverySnapshotLocked()
}
func (g *stripeGroup) deliverySnapshotLocked() latencyGovernorSnapshot {
	d := &g.delivery
	short := make(map[string]int, len(d.fastCarrierRanges))
	for account, count := range d.fastCarrierRanges {
		short[account] = count
	}
	d.init()
	return latencyGovernorSnapshot{Mode: string(d.policy.mode), BaselineMS: d.baselineMS, TargetMS: d.targetAge().Milliseconds(),
		AcknowledgementFloorMS: d.acknowledgementFloor().Milliseconds(), LastFastACKAgeMS: d.lastFastACKAgeMS,
		FastPathBaselineMS: d.fastPathBaseline().Milliseconds(), FastTargetMS: d.fastAgeBudget().Milliseconds(), PendingShortRangesByAccount: short,
		FastPressureRemainingMS: max(int64(0), time.Until(d.fastPressureUntil).Milliseconds()), FastPressureDecisions: d.fastPressureDecisions,
		ProbeAttempts: d.policy.probes, ProbeAccepted: d.policy.accepted, ProbeRejected: d.policy.rejected, ProbeActive: d.policy.mode == deliveryProbeUp,
		ObservedRTTMS: d.observedAgeMS, RateMbps: d.rateMbps, BulkRateMbps: d.bulkRateMbps, RateEWMA: d.policy.capacityMbps,
		DecisionCount: d.decisions, DownloadWindowBytes: d.window, DownloadBacklogBytes: d.bulk, FastFlightBytes: d.fast,
		FlightRanges: len(d.ranges), AdmissionWaiters: len(d.waiters)}
}

func (d *deliveryController) fastAgeBudget() time.Duration { return deliveryFastMaxAge }

func (d *deliveryController) fastPathBaseline() time.Duration {
	if d.fastBaselineCount < 4 {
		return 0
	}
	values := append([]time.Duration(nil), d.fastBaselineAges[:d.fastBaselineCount]...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	// The unloaded upper quartile includes ordinary radio jitter. Loaded or
	// repaired ranges never contribute, and the observation is capped at 2s.
	return min(2*time.Second, values[(len(values)-1)*3/4])
}
