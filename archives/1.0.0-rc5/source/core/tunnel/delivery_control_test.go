package tunnel

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/wire"
)

func deliveryJobs(t *testing.T, stream uint64, count, size int) []*datapath.Job {
	t.Helper()
	cfg := datapath.DefaultConfig(1, []string{"a01"})
	e, err := datapath.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Stop() })
	p, err := datapath.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Stop() })
	if _, err = e.Activate(1, stream); err != nil {
		t.Fatal(err)
	}
	w, err := p.Activate(1, stream)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Receive(1, w); err != nil {
		t.Fatal(err)
	}
	var jobs []*datapath.Job
	for i := 0; i < count; i++ {
		id, err := e.Queue(1, stream, bytes.Repeat([]byte{byte(i)}, size))
		if err != nil {
			t.Fatal(err)
		}
		f, err := e.Frame(1, id)
		if err != nil {
			t.Fatal(err)
		}
		n, err := datapath.Size(f, nil)
		if err != nil {
			t.Fatal(err)
		}
		j, err := e.Begin(1, id, "a01", n, 0)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, j)
	}
	return jobs
}
func committed(jobs []*datapath.Job, outcome flow.Outcome) []datapath.Completion {
	r := make([]datapath.Completion, len(jobs))
	for i := range r {
		r[i].Outcome = outcome
	}
	return r
}

func TestDeliveryFlightEarlyPartialACKUnknownAndStop(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	r, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	g.deliveryProgress(1, 16<<10)
	r.finish(jobs, committed(jobs, flow.CommitUnknown))
	if g.delivery.bulk != 48<<10 {
		t.Fatal("early/partial ACK lost or doubled flight", g.delivery.bulk)
	}
	g.deliveryProgress(1, 16<<10)
	if g.delivery.bulk != 48<<10 {
		t.Fatal("duplicate ACK returned credit")
	}
	g.deliveryProgress(1, 64<<10)
	if g.delivery.bulk != 0 || g.delivery.delivered != 64<<10 {
		t.Fatal("complete useful delivery", g.delivery)
	}
	other := deliveryJobs(t, 3, 1, 32<<10)
	x, err := g.reserveDelivery(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	x.finish(other, committed(other, flow.CommitUnknown))
	g.deliveryStopped(3)
	g.deliveryStopped(3)
	if g.delivery.bulk != 0 || g.delivery.delivered != 64<<10 {
		t.Fatal("stop counted delivery or leaked logical flight")
	}
	if _, err = g.reserveDelivery(context.Background(), other); !errors.Is(err, errDeliveryStopped) {
		t.Fatal("retired stream reused", err)
	}
}

func TestDeliveryFullBulkLeavesFastReserveAndCancellation(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.delivery.window = 64 << 10
	jobs := deliveryJobs(t, 1, 3, 32<<10)
	r, err := g.reserveDelivery(context.Background(), jobs[:2])
	if err != nil {
		t.Fatal(err)
	}
	r.finish(jobs[:2], committed(jobs[:2], flow.Committed))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = g.reserveDelivery(ctx, jobs[2:]); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("full flight escaped", err)
	}
	short := deliveryJobs(t, 5, 1, 64)
	fast, err := g.reserveDelivery(context.Background(), short)
	if err != nil {
		t.Fatal("short flow blocked by bulk", err)
	}
	fast.finish(short, committed(short, flow.NotCommitted))
	if g.delivery.fast != 0 || g.delivery.bulk != 64<<10 || len(g.delivery.waiters) != 0 {
		t.Fatal("reservation/cancel leaked", g.delivery)
	}
}

func TestDeliveryAbortedBeforePublicationReturnsOnlyItsLogicalFlight(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := deliveryJobs(t, 1, 1, 32<<10)
	b := deliveryJobs(t, 3, 1, 32<<10)
	r, err := g.reserveDelivery(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	s, err := g.reserveDelivery(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	s.finish(b, committed(b, flow.CommitUnknown))
	r.finish(a, nil)
	r.finish(a, nil)
	if g.delivery.bulk != 32<<10 || len(g.delivery.ranges) != 1 {
		t.Fatal("cancel returned sibling unknown flight")
	}
	if _, err = g.reserveDelivery(context.Background(), []*datapath.Job{b[0], b[0]}); err == nil {
		t.Fatal("duplicate batch accepted")
	}
}

func TestDeliveryControllerVirtualClockIdleResetsProbeWithoutChangingFlight(t *testing.T) {
	d := newDeliveryController()
	d.window = 2 * deliveryMinWindow
	initial := d.window
	now := time.Unix(100, 0)
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now}
	d.ranges[r.key] = r
	d.bulk = 32 << 10
	d.maintain(now.Add(2*time.Second), true)
	window := d.window
	if window >= initial || d.bulk != 32<<10 {
		t.Fatal("stalled delivery did not bound pressure")
	}
	d.progress(1, 32<<10, now.Add(3*time.Second))
	d.maintain(now.Add(4*time.Second), false)
	for i := 0; i < 100; i++ {
		d.maintain(now.Add(time.Duration(i+10)*time.Second), false)
	}
	if d.window != deliveryInitialWindow || d.bulk != 0 || d.delivered != 32<<10 {
		t.Fatal("drained epoch failed to reset its bounded probe or changed logical debt", d)
	}
	// Four useful, low-residence observations increase by one epoch only.
	for i := 0; i < 4; i++ {
		d.ages[i] = 200 * time.Millisecond
	}
	d.ageCount = 4
	d.maintain(now.Add(200*time.Second), true)
	if d.window != deliveryInitialWindow+64<<10 {
		t.Fatal("delivery recovery not independent of sparse throughput", d.window)
	}
}

func TestDeliveryStoppedQueuedBatchDoesNotFailSiblingOwners(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	g.deliveryStopped(1)
	l := &lane{stripe: g, ownerCtx: context.Background()}
	results := l.WriteBatch(jobs)
	if len(results) != len(jobs) {
		t.Fatal("missing completions")
	}
	for _, r := range results {
		if !r.NonFatal || r.Outcome != flow.NotCommitted || !errors.Is(r.Err, errDeliveryStopped) {
			t.Fatal("stream stop became carrier failure", r)
		}
	}
	if len(g.delivery.ranges) != 0 || len(g.delivery.waiters) != 0 {
		t.Fatal("unstarted range leaked")
	}
}

func TestDeliveryFutureBatchCannotPinWorkerAheadOfMissingPredecessor(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.delivery.window = 64 << 10
	jobs := deliveryJobs(t, 1, 4, 32<<10)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := g.reserveDelivery(ctx, jobs[2:]); !errors.Is(err, errDeliveryDeferred) {
		t.Fatal("future batch retained a worker", err)
	}
	if len(g.delivery.waiters) != 0 || g.delivery.bulk != 0 {
		t.Fatal("future offsets filled the admission pool")
	}
	predecessor, err := g.reserveDelivery(ctx, jobs[:2])
	if err != nil {
		t.Fatal("predecessor starved", err)
	}
	predecessor.finish(predecessor.jobs, committed(predecessor.jobs, flow.Committed))
	g.deliveryProgress(1, 64<<10)
	next, err := g.reserveDelivery(ctx, jobs[2:])
	if err != nil {
		t.Fatal("future batch stuck after ACK", err)
	}
	next.finish(next.jobs, committed(next.jobs, flow.Committed))
	g.deliveryProgress(1, 128<<10)
	if g.delivery.bulk != 0 || g.delivery.delivered != 128<<10 {
		t.Fatal("flight bytes leaked")
	}
}

func TestDeliveryUnstartedRetryKeepsItsOffsetCredit(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	first, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := committed(jobs, flow.NotCommitted)
	for i := range outcomes {
		outcomes[i].NonFatal = true
	}
	first.finish(jobs, outcomes)
	if g.delivery.bulk != 64<<10 {
		t.Fatal("unstarted retry opened a logical hole")
	}
	retry, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	retry.finish(jobs, committed(jobs, flow.Committed))
	g.deliveryProgress(1, 64<<10)
	if g.delivery.bulk != 0 || g.delivery.delivered != 64<<10 {
		t.Fatal("retry double charged", g.delivery)
	}
}

func TestDeliveryMixedStreamBatchYieldsFutureMembersBeforePublication(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	a := deliveryJobs(t, 1, 2, 32<<10)
	b := deliveryJobs(t, 3, 2, 32<<10)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	// Two mixed batches form opposite stream dependencies. Admission must
	// publish the ready prefixes independently instead of waiting atomically.
	first, err := g.reserveDelivery(ctx, []*datapath.Job{a[1], b[0]})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.jobs) != 1 || first.jobs[0] != b[0] {
		t.Fatal("future member admitted or ready prefix blocked")
	}
	first.finish(first.jobs, committed(first.jobs, flow.Committed))
	second, err := g.reserveDelivery(ctx, []*datapath.Job{a[0], b[1]})
	if err != nil {
		t.Fatal(err)
	}
	second.finish(second.jobs, committed(second.jobs, flow.Committed))
	last, err := g.reserveDelivery(ctx, []*datapath.Job{a[1]})
	if err != nil {
		t.Fatal(err)
	}
	last.finish(last.jobs, committed(last.jobs, flow.Committed))
	g.deliveryProgress(1, 64<<10)
	g.deliveryProgress(3, 64<<10)
	if g.delivery.bulk != 0 || g.delivery.delivered != 128<<10 {
		t.Fatal("mixed cohort leaked flight")
	}
}

func TestDeliveryPublishesPrefixThatFitsRemainingBudget(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	g.delivery.window = 48 << 10
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	first, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.jobs) != 1 || first.jobs[0] != jobs[0] || g.delivery.bulk != 32<<10 {
		t.Fatal("batch crossed window or required empty capacity")
	}
	first.finish(first.jobs, committed(first.jobs, flow.Committed))
	g.deliveryProgress(1, 32<<10)
	last, err := g.reserveDelivery(context.Background(), jobs[1:])
	if err != nil {
		t.Fatal(err)
	}
	last.finish(last.jobs, committed(last.jobs, flow.Committed))
	g.deliveryProgress(1, 64<<10)
	if g.delivery.bulk != 0 {
		t.Fatal("partial admission leaked")
	}
}

func TestDeliveryDeferredWaitGenerationWakesOnlyOnPredicateChange(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	jobs := deliveryJobs(t, 1, 2, 32<<10)
	_, err := g.reserveDelivery(context.Background(), jobs[1:])
	var deferred *deliveryDeferredError
	if !errors.As(err, &deferred) || !errors.Is(err, errDeliveryDeferred) {
		t.Fatal(err)
	}
	g.signal()
	select {
	case <-deferred.wake:
		t.Fatal("unrelated carrier signal woke future offset")
	default:
	}
	r, err := g.reserveDelivery(context.Background(), jobs[:1])
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-deferred.wake:
	default:
		t.Fatal("prefix admission lost broadcast")
	}
	r.finish(jobs[:1], committed(jobs[:1], flow.Committed))
	r, err = g.reserveDelivery(context.Background(), jobs[1:])
	if err != nil {
		t.Fatal(err)
	}
	r.finish(jobs[1:], committed(jobs[1:], flow.Committed))
}

func TestDeliveryPartialRetryRechecksEarlyACKBeforeParking(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	jobs := deliveryJobs(t, 1, 3, 32<<10)
	g.delivery.window = 64 << 10
	r, err := g.reserveDelivery(context.Background(), jobs)
	if err != nil || len(r.jobs) != 2 {
		t.Fatal(err)
	}
	wake := g.deliveryRetryWake(jobs[2])
	select {
	case <-wake:
		t.Fatal("full window did not park")
	default:
	}
	g.deliveryProgress(1, 64<<10)
	select {
	case <-wake:
	default:
		t.Fatal("early ACK did not wake")
	}
	wake = g.deliveryRetryWake(jobs[2])
	select {
	case <-wake:
	default:
		t.Fatal("already eligible member parked behind future event")
	}
	r.finish(r.jobs, committed(r.jobs, flow.Committed))
}
