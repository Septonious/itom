package tunnel

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func profileTestDiagnostic() *diagnostic { return &diagnostic{tracer: &traceLog{}} }
func TestRC101ProfileQuantilesAndBoundedClasses(t *testing.T) {
	d := profileTestDiagnostic()
	for i := 1; i <= 100; i++ {
		d.profileObserve(profileAppend, profileFast, time.Duration(i)*time.Millisecond, i == 100, i == 99)
	}
	d.profileObserve(-1, profileFast, time.Second, true, false)
	d.profileObserve(profileAppend, 99, time.Second, true, false)
	d.profileObserve(profileAppend, profileFast, -time.Second, true, false)
	s := d.profile.snapshot()
	if len(s.Rows) != 1 {
		t.Fatal(s)
	}
	r := s.Rows[0]
	if r.Count != 100 || r.Errors != 1 || r.Deferred != 1 || r.TotalUS != 5050000 || r.MeanUS != 50500 || r.MaxUS != 100000 || r.P50UpperUS != 64000 || r.P95UpperUS != 96000 || r.P99UpperUS != 100000 {
		t.Fatal(r)
	}
	d.profileObserve(profileAppend, profileFast, 70*time.Second, false, false)
	if d.profile.snapshot().Rows[0].MaxUS != 70000000 {
		t.Fatal("overflow bucket lost duration")
	}
	// Snapshots do not share mutable slices with later observations/callers.
	s.BucketUpperUS[0] = -100
	if d.profile.snapshot().BucketUpperUS[0] != 50 || r.Count != 100 {
		t.Fatal("mutable snapshot")
	}
}
func TestRC101ProfileDisabledAndConcurrentExport(t *testing.T) {
	var absent *diagnostic
	absent.profileObserve(profileACK, profileFast, time.Second, false, false)
	disabled := &diagnostic{}
	disabled.profileObserve(profileACK, profileFast, time.Second, false, false)
	if len(disabled.profile.snapshot().Rows) != 0 || disabled.snapshot().LatencyProfile != nil {
		t.Fatal("disabled recorded")
	}
	d := profileTestDiagnostic()
	var wg sync.WaitGroup
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				d.profileObserve(profileACK, profileBulk, 1234*time.Microsecond, false, false)
				if i%20 == 0 {
					if _, e := json.Marshal(d.snapshot()); e != nil {
						t.Error(e)
					}
				}
			}
		}()
	}
	wg.Wait()
	r := d.profile.snapshot().Rows[0]
	if r.Count != 4000 || r.TotalUS != 4936000 || r.MaxUS != 1234 {
		t.Fatal(r)
	}
}
func TestRC101ProfileACKObservesOnlyAuthenticatedProgress(t *testing.T) {
	d := profileTestDiagnostic()
	c := newDeliveryController()
	now := time.Now()
	k := deliveryKey{1, 0}
	c.bulk = 100
	c.ranges[k] = &deliveryRange{profile: d, key: k, start: 0, end: 100, admitted: now.Add(-90 * time.Millisecond), accepted: now.Add(-40 * time.Millisecond)}
	c.progress(1, 50, now)
	if len(d.profile.snapshot().Rows) != 0 || c.bulk != 50 {
		t.Fatal("partial range counted as complete")
	}
	c.progress(1, 100, now)
	s := d.profile.snapshot()
	if len(s.Rows) != 2 || s.Rows[0].Stage != "data_ack" || s.Rows[0].MaxUS != 90000 || s.Rows[1].MaxUS != 40000 || c.bulk != 0 || c.delivered != 100 {
		t.Fatal(s, c.bulk, c.delivered)
	}
	c.progress(1, 100, now.Add(time.Second))
	if d.profile.snapshot().Rows[0].Count != 1 {
		t.Fatal("duplicate ACK counted")
	}
	// Retirement never fabricates delivery timing or refunds physical debt.
	k = deliveryKey{2, 0}
	c.bulk = 100
	c.ranges[k] = &deliveryRange{profile: d, key: k, start: 0, end: 100, admitted: now}
	c.retire(2)
	if d.profile.snapshot().Rows[0].Count != 1 || c.delivered != 100 {
		t.Fatal("retirement became delivery")
	}
}
func TestRC101ProfileSummaryBound(t *testing.T) {
	d := profileTestDiagnostic()
	d.tracer.queue = make(chan traceEvent, 512)
	for i := 0; i < 100; i++ {
		d.profileBatchDone(time.Now().Add(-time.Second), nil, nil, &profileBatchTimeline{})
	}
	s := d.profile.snapshot()
	if s.Summaries != 1 || s.SuppressedSummaries != 99 || len(d.tracer.queue) != 1 || s.Rows[0].Count != 100 {
		t.Fatal(s)
	}
	var e traceEvent = <-d.tracer.queue
	if e.Event != "latency_batch" || len(e.Packet) != 0 || !json.Valid(e.Details) {
		t.Fatal(e)
	}
}
func BenchmarkRC101ProfileObserve(b *testing.B) {
	d := profileTestDiagnostic()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		d.profileObserve(profileAppend, profileFast, 80*time.Millisecond, false, false)
	}
}
