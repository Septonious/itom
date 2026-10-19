package tunnel

import (
	"encoding/json"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"itom.local/core/datapath"
	"itom.local/core/flow"
)

// Observation only. Fixed stages/classes/buckets bound memory independently of
// streams, accounts, errors and provider strings. Recording performs no IO,
// allocation, payload inspection, goroutine creation or transport decisions.
var profileEpoch = time.Now()

var profileBoundsUS = [...]int64{50, 100, 250, 500, 1000, 2000, 4000, 8000, 12000, 16000, 24000, 32000, 48000, 64000, 96000, 128000, 192000, 256000, 384000, 512000, 768000, 1000000, 1500000, 2000000, 3000000, 4000000, 6000000, 8000000, 12000000, 16000000, 32000000, 64000000}
var profileStageNames = [...]string{"pump_handoff", "delivery_admission", "bulk_preparation", "account_selection", "grant_wait", "physical_slot", "seal_pack", "journal_prepare", "wallet_spend", "append", "journal_complete", "physical_batch", "data_ack", "publication_to_ack", "fetch_empty", "fetch_data", "fetch_first_body", "receive_callback", "reader_wake", "imap_control", "imap_maintenance"}

const (
	profilePump = iota
	profileAdmission
	profilePreparation
	profileSelection
	profileGrant
	profileSlot
	profileSeal
	profilePrepare
	profileSpend
	profileAppend
	profileComplete
	profilePhysical
	profileACK
	profilePublishedACK
	profileFetchEmpty
	profileFetchData
	profileFirstBody
	profileCallback
	profileReaderWake
	profileControl
	profileMaintenance
)
const (
	profileBulk = iota
	profileFast
	profileRepair
	profileOther
)

var profileClasses = [...]string{"bulk", "interactive", "repair", "other"}

type profileHistogram struct {
	Count, Errors, Deferred, TotalUS uint64
	MaxUS                            int64
	Buckets                          [len(profileBoundsUS) + 1]uint64
}
type latencyProfile struct {
	mu                             sync.Mutex
	rows                           [len(profileStageNames)][len(profileClasses)]profileHistogram
	batches, summaries, suppressed atomic.Uint64
	nextSummary                    atomic.Int64
}
type latencyStageSnapshot struct {
	Stage      string                           `json:"stage"`
	Class      string                           `json:"class"`
	Count      uint64                           `json:"count"`
	Errors     uint64                           `json:"errors"`
	Deferred   uint64                           `json:"deferred"`
	TotalUS    uint64                           `json:"total_us"`
	MeanUS     uint64                           `json:"mean_us"`
	MaxUS      int64                            `json:"max_us"`
	P50UpperUS int64                            `json:"p50_upper_us"`
	P95UpperUS int64                            `json:"p95_upper_us"`
	P99UpperUS int64                            `json:"p99_upper_us"`
	Buckets    [len(profileBoundsUS) + 1]uint64 `json:"buckets"`
}
type latencyProfileSnapshot struct {
	Schema              int                    `json:"schema"`
	Clock               string                 `json:"clock"`
	Scope               string                 `json:"scope"`
	BucketUpperUS       []int64                `json:"bucket_upper_us"`
	Rows                []latencyStageSnapshot `json:"rows"`
	Summaries           uint64                 `json:"summaries"`
	SuppressedSummaries uint64                 `json:"suppressed_summaries"`
}

func (d *diagnostic) profileEnabled() bool { return d != nil && d.tracer != nil && !d.metricsOnly }
func (d *diagnostic) profileObserve(stage, class int, elapsed time.Duration, failed, deferred bool) {
	if !d.profileEnabled() || stage < 0 || stage >= len(profileStageNames) || class < 0 || class >= len(profileClasses) || elapsed < 0 {
		return
	}
	us := elapsed.Microseconds()
	bucket := sort.Search(len(profileBoundsUS), func(i int) bool { return profileBoundsUS[i] >= us })
	p := &d.profile
	p.mu.Lock()
	h := &p.rows[stage][class]
	h.Count++
	h.TotalUS += uint64(us)
	h.MaxUS = max(h.MaxUS, us)
	h.Buckets[bucket]++
	if failed {
		h.Errors++
	}
	if deferred {
		h.Deferred++
	}
	p.mu.Unlock()
}
func (p *latencyProfile) snapshot() *latencyProfileSnapshot {
	s := &latencyProfileSnapshot{Schema: 1, Clock: "local_monotonic_duration_us", Scope: "endpoint_cumulative_since_process_start; per-stage observation counts, not additive RTT", BucketUpperUS: append([]int64(nil), profileBoundsUS[:]...), Summaries: p.summaries.Load(), SuppressedSummaries: p.suppressed.Load()}
	// The final bucket is unbounded. Its percentile is capped by exact observed
	// max, as are normal buckets. Percentiles are upper-bound estimates, not exact.
	s.BucketUpperUS = append(s.BucketUpperUS, -1)
	p.mu.Lock()
	rows := p.rows
	p.mu.Unlock()
	for stage := range rows {
		for class, h := range rows[stage] {
			if h.Count == 0 {
				continue
			}
			quantile := func(percent uint64) int64 {
				rank := h.Count/100*percent + (h.Count%100*percent+99)/100
				var n uint64
				for i, count := range h.Buckets {
					n += count
					if n >= rank {
						if i == len(profileBoundsUS) {
							return h.MaxUS
						}
						return min(profileBoundsUS[i], h.MaxUS)
					}
				}
				return h.MaxUS
			}
			s.Rows = append(s.Rows, latencyStageSnapshot{profileStageNames[stage], profileClasses[class], h.Count, h.Errors, h.Deferred, h.TotalUS, h.TotalUS / h.Count, h.MaxUS, quantile(50), quantile(95), quantile(99), h.Buckets})
		}
	}
	return s
}
func profileClass(jobs []*datapath.Job) int {
	class := profileFast
	for _, j := range jobs {
		if j.Attempt().Repair {
			return profileRepair
		}
		if !j.Interactive() {
			class = profileBulk
		}
	}
	return class
}
func (d *diagnostic) profileHandoff(jobs []*datapath.Job) {
	if !d.profileEnabled() {
		return
	}
	now := time.Now()
	var oldest time.Duration
	for _, j := range jobs {
		oldest = max(oldest, j.QueueWait(now))
	}
	d.profileObserve(profilePump, profileClass(jobs), oldest, false, false)
}

// Physical-batch timelines contain only local durations and opaque identifiers.
// Existing packet/frame metadata joins the two endpoints; clocks are never
// subtracted across machines. At most four extra summaries/second/account.
type profileBatchTimeline struct {
	Batch        any    `json:"batch"`
	Owner        string `json:"owner_account"`
	Class        string `json:"class"`
	Records      int    `json:"records"`
	PayloadBytes int64  `json:"payload_bytes"`
	FirstStream  uint64 `json:"first_stream"`
	FirstOffset  uint64 `json:"first_offset"`
	SealPackUS   int64  `json:"seal_pack_us"`
	GrantUS      int64  `json:"grant_wait_us"`
	SlotUS       int64  `json:"physical_slot_us"`
	DurableUS    int64  `json:"durable_prepare_us"`
	AppendUS     int64  `json:"append_us"`
	FinalizeUS   int64  `json:"finalize_us"`
	TotalUS      int64  `json:"total_us"`
	Unknown      bool   `json:"unknown_append"`
}

func (d *diagnostic) profileBatchDone(start time.Time, jobs []*datapath.Job, results []datapath.Completion, span *profileBatchTimeline) {
	if !d.profileEnabled() {
		return
	}
	elapsed := time.Since(start)
	failed := false
	for _, result := range results {
		failed = failed || result.Err != nil
		span.Unknown = span.Unknown || result.Outcome == flow.CommitUnknown
	}
	d.profileObserve(profilePhysical, profileClass(jobs), elapsed, failed, false)
	n := d.profile.batches.Add(1)
	if !failed && elapsed < 100*time.Millisecond && n%64 != 1 {
		return
	}
	now := time.Since(profileEpoch).Nanoseconds()
	next := d.profile.nextSummary.Load()
	if now < next || !d.profile.nextSummary.CompareAndSwap(next, now+int64(250*time.Millisecond)) {
		d.profile.suppressed.Add(1)
		return
	}
	span.TotalUS = elapsed.Microseconds()
	d.profile.summaries.Add(1)
	d.profileSummary(span, failed)
}

func (d *diagnostic) profileSummary(span *profileBatchTimeline, failed bool) {
	raw, _ := json.Marshal(span)
	code := "OK"
	if failed {
		code = "FAILED"
	}
	d.trace(traceEvent{Event: "latency_batch", Name: span.Class, Stream: span.FirstStream, Offset: span.FirstOffset, DurationUS: span.TotalUS, Code: code, Details: raw})
}
