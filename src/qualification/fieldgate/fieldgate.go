// Package fieldgate checks field-like responsiveness separately from the old
// transport-survival gates. All times are receiver-side or real round trips.
package fieldgate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

type Distribution struct {
	Count    int     `json:"count"`
	Median   float64 `json:"median_ms"`
	P95      float64 `json:"p95_ms"`
	Max      float64 `json:"max_ms"`
	Under100 int     `json:"under_100_ms"`
}

func Summarize(values []float64) (Distribution, error) {
	if len(values) == 0 {
		return Distribution{}, fmt.Errorf("no samples")
	}
	v := append([]float64(nil), values...)
	d := Distribution{Count: len(v)}
	for _, x := range v {
		if x <= 0 || math.IsNaN(x) || math.IsInf(x, 0) {
			return Distribution{}, fmt.Errorf("invalid RTT %v", x)
		}
		if x < 100 {
			d.Under100++
		}
	}
	sort.Float64s(v)
	d.Median = (v[(len(v)-1)/2] + v[len(v)/2]) / 2
	d.P95 = v[int(math.Ceil(.95*float64(len(v))))-1]
	d.Max = v[len(v)-1]
	return d, nil
}

type Transfer struct {
	Bytes   uint64             `json:"verified_bytes"`
	First   map[string]float64 `json:"first_verified_ms_by_stream"`
	Rates   []float64          `json:"half_second_mbps"`
	MaxGap  float64            `json:"max_receiver_gap_after_ramp_ms"`
	Goodput float64            `json:"full_phase_mbps"`
}
type Phase struct {
	Name               string    `json:"name"`
	Streams            int       `json:"streams"`
	Success            bool      `json:"success"`
	CompletedDownloads int       `json:"completed_downloads"`
	CompletedUploads   int       `json:"completed_uploads"`
	Elapsed            float64   `json:"elapsed_seconds"`
	SendSeconds        float64   `json:"send_seconds"`
	Loaded             []float64 `json:"loaded_ms"`
	Download           Transfer  `json:"download"`
	Upload             Transfer  `json:"upload"`
}
type NativeCounters struct {
	OpenAttempts    int `json:"OpenAttempts"`
	OpenEstablished int `json:"OpenEstablished"`
	OpenFailures    int `json:"OpenFailures"`
	OpenTimeouts    int `json:"OpenTimeouts"`
	WorkerRefusals  int `json:"WorkerRefusals"`
	DNSFailures     int `json:"DNSFailures"`
}
type Result struct {
	Light       []LightPhase    `json:"light"`
	Granted     []string        `json:"granted_accounts"`
	Accounts    []AccountSample `json:"account_samples"`
	Success     bool            `json:"success"`
	Stage       string          `json:"stage"`
	Error       string          `json:"error"`
	Idle        []float64       `json:"IdleMS"`
	After       []float64       `json:"AfterMS"`
	Phases      []Phase         `json:"Phases"`
	Diagnostics struct {
		Revision string
		Native   *NativeCounters
	} `json:"diagnostics"`
}
type traceRecord struct {
	Event       string
	SampledAtMS int64
	Client      *struct {
		RTTMillis       int64
		ActiveLines     int
		TotalLines      int
		GrantNegotiated bool
	}
}
type DirectionMetrics struct {
	FirstMax float64 `json:"last_stream_first_payload_ms"`
	Started  int     `json:"started_streams"`
	Goodput  float64 `json:"goodput_mbps"`
}
type Report struct {
	Revision   string                      `json:"revision"`
	Transport  Distribution                `json:"ready_transport"`
	Idle       Distribution                `json:"idle_application"`
	After      Distribution                `json:"post_application"`
	Problems   []string                    `json:"problems"`
	Directions map[string]DirectionMetrics `json:"directions"`
}

// ReadResult verifies the actual production revision and gathers ready-state
// transport RTT samples, not the best ping or a possibly stale final snapshot.
// Multiple rotated files are deduplicated using their original sample times.
func ReadResult(dir string) (Result, []float64, error) {
	var r Result
	b, e := os.ReadFile(filepath.Join(dir, "result.json"))
	if e != nil {
		return r, nil, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, nil, e
	}
	paths, e := filepath.Glob(filepath.Join(dir, "candidate-client-trace.jsonl*"))
	if e != nil {
		return r, nil, e
	}
	seen := map[int64]float64{}
	for _, p := range paths {
		f, e := os.Open(p)
		if e != nil {
			return r, nil, e
		}
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 64<<10), 8<<20)
		for s.Scan() {
			var x traceRecord
			if e = json.Unmarshal(s.Bytes(), &x); e != nil {
				f.Close()
				return r, nil, fmt.Errorf("%s: %w", p, e)
			}
			if x.Event == "client_runtime" && x.Client != nil && x.Client.GrantNegotiated && x.Client.ActiveLines == x.Client.TotalLines && x.Client.TotalLines == 32 {
				if x.SampledAtMS <= 0 {
					f.Close()
					return r, nil, fmt.Errorf("invalid trace timestamp")
				}
				ms := float64(x.Client.RTTMillis)
				if old, ok := seen[x.SampledAtMS]; ok && old != ms {
					f.Close()
					return r, nil, fmt.Errorf("conflicting trace sample")
				}
				seen[x.SampledAtMS] = ms
			}
		}
		e = s.Err()
		f.Close()
		if e != nil {
			return r, nil, e
		}
	}
	keys := make([]int64, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	v := make([]float64, 0, len(keys))
	for _, k := range keys {
		v = append(v, seen[k])
	}
	return r, v, nil
}

// Assess applies a strict sub-100 ms target on normal-link quiet application
// latency and ready-state transport latency. Delayed-provider and outage cases
// retain measurements for comparison; their forced propagation delay is not
// confused with a transport startup failure. Recovery cannot erase a stall.
func Assess(r Result, transport []float64, cellular, normal bool) Report {
	out := Report{Revision: r.Diagnostics.Revision, Problems: []string{}, Directions: map[string]DirectionMetrics{}}
	fail := func(f string, a ...any) { out.Problems = append(out.Problems, fmt.Sprintf(f, a...)) }
	checkRTT := func(name string, v []float64, minN int, strict bool) Distribution {
		d, e := Summarize(v)
		if e != nil || len(v) < minN {
			fail("%s: insufficient/invalid RTT evidence (%d, %v)", name, len(v), e)
			return d
		}
		if strict && d.Max > 200 {
			fail("%s: maximum %.2f ms exceeds 200 ms", name, d.Max)
		}
		if strict && d.P95 >= 100 {
			fail("%s: p95 %.2f ms is not below 100 ms", name, d.P95)
		}
		return d
	}
	out.Transport = checkRTT("ready transport", transport, 10, normal)
	out.Idle = checkRTT("idle app", r.Idle, 6, normal)
	out.After = checkRTT("post-load app", r.After, 6, normal)
	// Fixture connections all target known-permitted local application servers.
	// A failed open cannot be hidden by a successful retry or a fast old stream.
	native := r.Diagnostics.Native
	if native == nil || native.OpenAttempts < 8 || native.OpenEstablished < 8 {
		fail("missing native application-open evidence")
	} else if native.OpenFailures != 0 || native.OpenTimeouts != 0 || native.WorkerRefusals != 0 || native.DNSFailures != 0 || native.OpenEstablished != native.OpenAttempts {
		fail("native application opens: %d/%d established, %d failures, %d timeouts, %d refusals, %d DNS failures", native.OpenEstablished, native.OpenAttempts, native.OpenFailures, native.OpenTimeouts, native.WorkerRefusals, native.DNSFailures)
	}
	if !r.Success || r.Stage != "complete" {
		fail("incomplete application workload: %s %s", r.Stage, r.Error)
	}
	want := []string{"download", "upload"}
	if cellular {
		want = []string{"duplex"}
	}
	if len(r.Phases) != len(want) {
		fail("expected phases %v, got %d", want, len(r.Phases))
	}
	for i, p := range r.Phases {
		if i >= len(want) || p.Name != want[i] {
			fail("unexpected phase %s", p.Name)
		}
		if !p.Success || p.Streams != 8 {
			fail("%s: incomplete phase or wrong stream count", p.Name)
		}
		if normal {
			checkRTT(p.Name+" loaded app", p.Loaded, 3, true)
		}
		if p.Name == "download" || p.Name == "duplex" {
			if p.CompletedDownloads != 8 {
				fail("%s: %d/8 completed downloads", p.Name, p.CompletedDownloads)
			}
			out.Directions[p.Name+" download"] = directionMetrics(p.Download)
			checkTransfer(p.Name+" download", p.Download, p.SendSeconds, cellular, fail)
			if normal {
				checkHomeGoodput(p.Name+" download", p.Download, p.SendSeconds, p.Elapsed, fail)
			}
		}
		if p.Name == "upload" || p.Name == "duplex" {
			if p.CompletedUploads != 8 {
				fail("%s: %d/8 completed uploads", p.Name, p.CompletedUploads)
			}
			out.Directions[p.Name+" upload"] = directionMetrics(p.Upload)
			checkTransfer(p.Name+" upload", p.Upload, p.SendSeconds, cellular, fail)
			if normal {
				checkHomeGoodput(p.Name+" upload", p.Upload, p.SendSeconds, p.Elapsed, fail)
			}
		}
	}
	if normal {
		checkHomeLight(r, fail, checkRTT)
	}
	return out
}

func directionMetrics(d Transfer) DirectionMetrics {
	m := DirectionMetrics{Started: len(d.First), Goodput: d.Goodput}
	for _, v := range d.First {
		if v > m.FirstMax {
			m.FirstMax = v
		}
	}
	return m
}

func checkTransfer(name string, d Transfer, seconds float64, cellular bool, fail func(string, ...any)) {
	limit := 2000.0
	if cellular {
		limit = 3000
	}
	if len(d.First) != 8 {
		fail("%s: only %d/8 streams produced verified payload", name, len(d.First))
	}
	for i := 0; i < 8; i++ {
		ms, ok := d.First[strconv.Itoa(i)]
		if !ok || ms <= 0 || math.IsNaN(ms) || math.IsInf(ms, 0) || ms > limit {
			fail("%s: stream %d first payload %.2f ms (limit %.0f, present=%t)", name, i, ms, limit, ok)
		}
	}
	if d.Bytes == 0 {
		fail("%s: no verified bytes", name)
	}
	need := int(math.Ceil(seconds * 2))
	if seconds <= 0 || len(d.Rates) < need {
		fail("%s: incomplete receiver buckets", name)
		return
	}
	for i, x := range d.Rates[:need] {
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
			fail("%s: invalid rate at %.1fs", name, float64(i)/2)
		}
		// For cellular, only require continuous progress before the scripted
		// degradation and after its 10-second recovery allowance.
		stable := !cellular || i < 16 || i >= 80
		if stable && i >= 4 && x == 0 {
			fail("%s: no payload at %.1fs", name, float64(i)/2)
		}
	}
	if !cellular && (math.IsNaN(d.MaxGap) || math.IsInf(d.MaxGap, 0) || d.MaxGap < 0 || d.MaxGap >= 500) {
		fail("%s: receiver stalled %.2f ms after ramp", name, d.MaxGap)
	}
}

// Compare is an additional RC7-relative gate. These small provisional margins
// are not replacements for the absolute target or repeated field measurements.
func Compare(base, candidate Report) []string {
	var out []string
	for _, v := range []struct {
		name string
		b, c Distribution
	}{{"ready transport", base.Transport, candidate.Transport}, {"idle app", base.Idle, candidate.Idle}, {"post-load app", base.After, candidate.After}} {
		if v.b.Count < 6 || v.c.Count < 6 {
			out = append(out, v.name+": missing comparison samples")
			continue
		}
		if v.c.Median > v.b.Median+math.Max(10, .1*v.b.Median) {
			out = append(out, fmt.Sprintf("%s median regressed %.2f -> %.2f ms", v.name, v.b.Median, v.c.Median))
		}
		if v.c.P95 > v.b.P95+math.Max(15, .15*v.b.P95) {
			out = append(out, fmt.Sprintf("%s p95 regressed %.2f -> %.2f ms", v.name, v.b.P95, v.c.P95))
		}
	}
	for name, b := range base.Directions {
		c, ok := candidate.Directions[name]
		if !ok || b.Started != 8 || c.Started != 8 {
			out = append(out, name+": missing eight-stream startup evidence")
			continue
		}
		if c.FirstMax > b.FirstMax+math.Max(500, .25*b.FirstMax) {
			out = append(out, fmt.Sprintf("%s last-stream startup regressed %.1f -> %.1f ms", name, b.FirstMax, c.FirstMax))
		}
		if b.Goodput > 0 && c.Goodput < .9*b.Goodput {
			out = append(out, fmt.Sprintf("%s goodput regressed %.2f -> %.2f Mbps", name, b.Goodput, c.Goodput))
		}
	}
	sort.Strings(out)
	return out
}
