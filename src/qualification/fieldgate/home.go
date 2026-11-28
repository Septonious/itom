package fieldgate

import (
	"fmt"
	"math"
)

type LightPhase struct {
	Name    string    `json:"name"`
	Streams int       `json:"streams"`
	Rate    int       `json:"offered_bps"`
	Bytes   int       `json:"verified_echo_bytes"`
	Elapsed float64   `json:"elapsed_s"`
	App     []float64 `json:"application_ms"`
}
type AccountSample struct {
	Phase   string  `json:"phase"`
	Account string  `json:"account"`
	RTT     float64 `json:"rtt_ms"`
	Error   string  `json:"error"`
}

// Normal-home qualification requires measured application goodput, not
// a reported rate or a successful end-of-test drain. Cellular stays separate.
func checkHomeGoodput(name string, d Transfer, seconds, elapsed float64, fail func(string, ...any)) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 15 || seconds != math.Trunc(seconds) || seconds > 3600 || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) || elapsed < seconds {
		fail("%s: insufficient/invalid sustained workload duration", name)
		return
	}
	actual := float64(d.Bytes) * 8 / elapsed / 1e6
	if actual < 200 || math.IsNaN(d.Goodput) || math.IsInf(d.Goodput, 0) || math.Abs(actual-d.Goodput) > .01 {
		fail("%s: verified goodput %.2f Mbps (reported %.2f)", name, actual, d.Goodput)
	}
	n := int(seconds * 2)
	if len(d.Rates) < n {
		return
	}
	ramp := false
	for i := 0; i+4 <= 10; i++ {
		if d.Rates[i] >= 200 && d.Rates[i+1] >= 200 && d.Rates[i+2] >= 200 && d.Rates[i+3] >= 200 {
			ramp = true
			break
		}
	}
	if !ramp {
		fail("%s: 200 Mbps sustained ramp missing within 5s", name)
	}
	for i := 10; i+20 <= n; i++ {
		var sum float64
		for _, v := range d.Rates[i : i+20] {
			sum += v
		}
		if sum/20 < 200 {
			fail("%s: post-ramp 10s goodput %.2f Mbps", name, sum/20)
			break
		}
	}
}

func checkHomeLight(r Result, fail func(string, ...any), rtts func(string, []float64, int, bool) Distribution) {
	type workload struct{ rate, streams int }
	expected := map[string]workload{}
	for _, v := range []workload{{50000, 1}, {100000, 1}, {500000, 4}, {2000000, 8}, {5000000, 4}} {
		expected[fmt.Sprintf("burst_%dk", v.rate/1000)] = v
		expected[fmt.Sprintf("idle_after_%dk", v.rate/1000)] = workload{}
	}
	stages := map[string]bool{"idle": true, "download": true, "upload": true, "post_load": true}
	counts := map[string]int{}
	for _, p := range r.Light {
		w, ok := expected[p.Name]
		if !ok {
			fail("unexpected light phase %s", p.Name)
			continue
		}
		counts[p.Name]++
		stages[p.Name] = true
		if p.Streams != w.streams || p.Rate != w.rate || p.Bytes != w.rate*3/8 || p.Elapsed < 3 || math.IsNaN(p.Elapsed) || math.IsInf(p.Elapsed, 0) {
			fail("%s: incomplete light workload", p.Name)
		}
		rtts(p.Name+" app", p.App, 3, true)
	}
	for name := range expected {
		stages[name] = true
		if counts[name] < 2 || counts[name] != counts["burst_50k"] {
			fail("%s: missing repeated light phases", name)
		}
	}
	granted := map[string]bool{}
	for _, a := range r.Granted {
		if a != "" {
			granted[a] = true
		}
	}
	if len(granted) != 8 || len(r.Granted) != 8 {
		fail("missing eight distinct account probes")
		return
	}
	samples := map[string]map[string][]float64{}
	for _, s := range r.Accounts {
		if !stages[s.Phase] || !granted[s.Account] || s.Error != "" {
			fail("invalid account probe %s/%s", s.Phase, s.Account)
			continue
		}
		if samples[s.Phase] == nil {
			samples[s.Phase] = map[string][]float64{}
		}
		samples[s.Phase][s.Account] = append(samples[s.Phase][s.Account], s.RTT)
	}
	for stage := range stages {
		for a := range granted {
			rtts(stage+" account "+a, samples[stage][a], 1, true)
		}
	}
}
