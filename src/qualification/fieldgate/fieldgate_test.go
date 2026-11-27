package fieldgate

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func healthy() Result {
	var r Result
	r.Success = true
	r.Stage = "complete"
	r.Diagnostics.Native = &NativeCounters{OpenAttempts: 10, OpenEstablished: 10}
	r.Idle = []float64{61, 65, 70, 75, 72, 67}
	r.After = r.Idle
	for _, name := range []string{"download", "upload"} {
		d := Transfer{Bytes: 100_000_000, First: map[string]float64{}, Rates: make([]float64, 30), MaxGap: 75, Goodput: 250}
		for i := 0; i < 8; i++ {
			d.First[strconv.Itoa(i)] = 400
		}
		for i := range d.Rates {
			d.Rates[i] = 250
		}
		p := Phase{Name: name, Streams: 8, Success: true, SendSeconds: 15}
		if name == "download" {
			p.Download = d
			p.CompletedDownloads = 8
		} else {
			p.Upload = d
		}
		r.Phases = append(r.Phases, p)
	}
	return r
}
func ready(ms float64) []float64 {
	v := make([]float64, 20)
	for i := range v {
		v[i] = ms
	}
	return v
}
func has(p []string, s string) bool { return strings.Contains(strings.Join(p, "\n"), s) }
func TestSub100TransportRejectsObservedFieldFloor(t *testing.T) {
	// Anonymized positive RTT samples from the RC15 field capture, sorted by
	// original sample timestamp. No mailboxes, credentials or raw trace data.
	v := []float64{153, 139, 139, 151, 187, 137, 147, 134, 128, 129, 131, 163, 137, 134, 139, 150, 150, 136, 140, 222, 157, 156, 147, 144, 152, 151, 145, 136, 163, 132, 182, 158, 207, 203, 249, 194, 164, 181, 143, 136, 173, 137, 141, 192, 164, 192, 178, 190, 160, 172, 171, 144, 214, 172, 172, 182, 157, 231, 229, 168, 191, 141, 160, 165, 142, 175, 142, 214, 186, 147}
	a := Assess(healthy(), v, false, true)
	if !has(a.Problems, "ready transport: p95") {
		t.Fatal(a)
	}
	if a.Transport.Count != 70 || a.Transport.Under100 != 0 || a.Transport.Median != 157 || a.Transport.Max != 249 {
		t.Fatal(a.Transport)
	}
}
func TestNormalLinkAcceptsSub100AndCompleteSpeedtest(t *testing.T) {
	if a := Assess(healthy(), ready(72), false, true); len(a.Problems) != 0 {
		t.Fatal(a.Problems)
	}
}
func TestSpeedtestCannotPassByRecoveringLater(t *testing.T) {
	r := healthy()
	d := &r.Phases[0].Download
	for i := 0; i < 8; i++ {
		d.First[strconv.Itoa(i)] = 5500
	}
	for i := 0; i < 11; i++ {
		d.Rates[i] = 0
	}
	if a := Assess(r, ready(72), false, true); !has(a.Problems, "first payload") || !has(a.Problems, "no payload") {
		t.Fatal(a.Problems)
	}
}
func TestMissingStreamNotHiddenByAggregateThroughput(t *testing.T) {
	r := healthy()
	delete(r.Phases[1].Upload.First, "7")
	if a := Assess(r, ready(72), false, true); !has(a.Problems, "7/8 streams") {
		t.Fatal(a.Problems)
	}
}
func TestMissingAndInvalidEvidenceFailsClosed(t *testing.T) {
	r := healthy()
	r.After = nil
	r.Phases = nil
	if a := Assess(r, nil, false, true); len(a.Problems) < 3 {
		t.Fatal(a.Problems)
	}
	if _, e := Summarize([]float64{75, math.NaN()}); e == nil {
		t.Fatal("invalid RTT accepted")
	}
}
func TestExactly100IsNotSub100(t *testing.T) {
	if a := Assess(healthy(), ready(100), false, true); !has(a.Problems, "not below 100") {
		t.Fatal(a)
	}
}
func TestFailedNativeOpensCannotHideBehindRetry(t *testing.T) {
	r := healthy()
	// The field snapshot had 199 failed opens out of 295 attempts. The
	// benchmark gates known-permitted targets, not arbitrary field websites.
	r.Diagnostics.Native = &NativeCounters{OpenAttempts: 295, OpenEstablished: 96, OpenFailures: 199}
	if a := Assess(r, ready(72), false, true); !has(a.Problems, "199 failures") {
		t.Fatal(a.Problems)
	}
}
func TestLoadStallDespiteFullTransfer(t *testing.T) {
	r := healthy()
	r.Phases[1].Upload.MaxGap = 501
	if a := Assess(r, ready(80), false, true); !has(a.Problems, "receiver stalled") {
		t.Fatal(a)
	}
}
func TestReferenceRegressionDetectedEvenOnDelayedFixture(t *testing.T) {
	a := Assess(healthy(), ready(101), false, false)
	b := Assess(healthy(), ready(157), false, false)
	if !has(Compare(a, b), "ready transport median regressed") {
		t.Fatal(Compare(a, b))
	}
}
func TestRC7RelativeGateRejectsSlowStartAndGoodput(t *testing.T) {
	b := Assess(healthy(), ready(72), false, true)
	r := healthy()
	r.Phases[0].Download.First["7"] = 1500
	r.Phases[0].Download.Goodput = 200
	c := Assess(r, ready(72), false, true)
	if p := Compare(b, c); !has(p, "last-stream startup regressed") || !has(p, "goodput regressed") {
		t.Fatal(p)
	}
}
func TestCellularOutageDoesNotExcuseInitialOrPersistentStall(t *testing.T) {
	r := healthy()
	p := r.Phases[0]
	p.Name = "duplex"
	p.Upload = r.Phases[1].Upload
	p.SendSeconds = 55
	for _, d := range []*Transfer{&p.Download, &p.Upload} {
		d.Rates = make([]float64, 110)
		for i := range d.Rates {
			if i < 56 || i >= 60 {
				d.Rates[i] = 5
			}
		}
	}
	r.Phases = []Phase{p}
	if a := Assess(r, ready(80), true, false); len(a.Problems) != 0 {
		t.Fatal(a.Problems)
	}
	r.Phases[0].Download.Rates[81] = 0
	if a := Assess(r, ready(80), true, false); !has(a.Problems, "no payload at 40.5s") {
		t.Fatal(a.Problems)
	}
}
func TestReadResultDeduplicatesRotationsAndIgnoresUnready(t *testing.T) {
	dir := t.TempDir()
	b, _ := json.Marshal(healthy())
	if e := os.WriteFile(filepath.Join(dir, "result.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	line := `{"Event":"client_runtime","SampledAtMS":1,"Client":{"RTTMillis":72,"ActiveLines":32,"TotalLines":32,"GrantNegotiated":true}}` + "\n"
	unready := strings.ReplaceAll(strings.ReplaceAll(line, `"SampledAtMS":1`, `"SampledAtMS":2`), `"ActiveLines":32`, `"ActiveLines":4`)
	for _, suffix := range []string{"", ".1"} {
		if e := os.WriteFile(filepath.Join(dir, "candidate-client-trace.jsonl"+suffix), []byte(line+unready), 0600); e != nil {
			t.Fatal(e)
		}
	}
	_, v, e := ReadResult(dir)
	if e != nil || len(v) != 1 || v[0] != 72 {
		t.Fatal(v, e)
	}
}

// This test consumes a real benchmark run. The command must set the path;
// unit-test runs intentionally skip replay rather than reporting a fake pass.
func TestRecordedFieldQualification(t *testing.T) {
	dir := os.Getenv("ITOM_FIELD_RESULT")
	if dir == "" {
		t.Skip("set ITOM_FIELD_RESULT to a completed benchmark directory")
	}
	r, v, e := ReadResult(dir)
	if e != nil {
		t.Fatal(e)
	}
	if want := os.Getenv("ITOM_FIELD_REVISION"); want == "" || r.Diagnostics.Revision != want {
		t.Fatalf("unexpected revision %q, expected %q", r.Diagnostics.Revision, want)
	}
	a := Assess(r, v, os.Getenv("ITOM_FIELD_CELLULAR") == "1", os.Getenv("ITOM_FIELD_NORMAL") == "1")
	b, _ := json.Marshal(a)
	t.Log(string(b))
	if len(a.Problems) != 0 {
		t.Fatal(strings.Join(a.Problems, "\n"))
	}
}

// Compare actual paired captures, independent of absolute pass/fail.
func TestRecordedRC7Comparison(t *testing.T) {
	base, candidate := os.Getenv("ITOM_RC7_RESULT"), os.Getenv("ITOM_FIELD_RESULT")
	if base == "" || candidate == "" {
		t.Skip("set ITOM_RC7_RESULT and ITOM_FIELD_RESULT to matched captures")
	}
	b, bv, e := ReadResult(base)
	if e != nil {
		t.Fatal(e)
	}
	c, cv, e := ReadResult(candidate)
	if e != nil {
		t.Fatal(e)
	}
	if b.Diagnostics.Revision != "itom-1-5-1-rc7-dev" || c.Diagnostics.Revision != os.Getenv("ITOM_FIELD_REVISION") {
		t.Fatal("unexpected source revisions")
	}
	cell, normal := os.Getenv("ITOM_FIELD_CELLULAR") == "1", os.Getenv("ITOM_FIELD_NORMAL") == "1"
	p := Compare(Assess(b, bv, cell, normal), Assess(c, cv, cell, normal))
	if len(p) > 0 {
		t.Fatal(strings.Join(p, "\n"))
	}
}
