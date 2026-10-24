// Package qualification checks raw application receiver observations. Protocol
// fixture rates, APPEND peaks and successful-only means are not accepted here.
package qualification

import (
	"fmt"
	"time"
)

const RateBytesPerSecond uint64 = 12500000 // 100 decimal Mbps useful payload

type Arrival struct {
	At    time.Duration
	Bytes uint64
}
type Transfer struct {
	Duration  time.Duration
	Sustained bool
	Arrivals  []Arrival
}

// CheckTransfer includes startup time in the full-phase mean, checks exact
// event-aligned rolling windows, and detects every >=500ms useful-byte stall.
// It does not smooth samples, discard the ramp or count retries as success.
func CheckTransfer(t Transfer) []string {
	var failures []string
	minimum := 12 * time.Second
	if t.Sustained {
		minimum = 60 * time.Second
	}
	if t.Duration != minimum {
		return []string{fmt.Sprintf("duration must be %s", minimum)}
	}
	if len(t.Arrivals) == 0 {
		return []string{"missing verified application receiver arrivals"}
	}
	prefix := make([]uint64, len(t.Arrivals)+1)
	last := time.Duration(-1)
	for i, a := range t.Arrivals {
		if a.At < 0 || a.At > t.Duration || a.At < last || a.Bytes == 0 || a.Bytes > 1<<30 || prefix[i] > ^uint64(0)-a.Bytes {
			return []string{"invalid/incomplete receiver timeline"}
		}
		prefix[i+1] = prefix[i] + a.Bytes
		last = a.At
	}
	bytesIn := func(start, end time.Duration) uint64 {
		// [start,end), including arrivals at start and excluding those at end.
		left, right := 0, len(t.Arrivals)
		for left < right {
			m := (left + right) / 2
			if t.Arrivals[m].At < start {
				left = m + 1
			} else {
				right = m
			}
		}
		a := left
		left, right = 0, len(t.Arrivals)
		for left < right {
			m := (left + right) / 2
			if t.Arrivals[m].At < end {
				left = m + 1
			} else {
				right = m
			}
		}
		return prefix[left] - prefix[a]
	}
	required := func(d time.Duration) uint64 { return RateBytesPerSecond * uint64(d) / uint64(time.Second) }
	if bytesIn(0, t.Duration) < required(t.Duration) {
		failures = append(failures, "full-phase useful mean below 100 Mbps")
	}
	if bytesIn(t.Duration-2*time.Second, t.Duration) < required(2*time.Second) {
		failures = append(failures, "last two seconds below 100 Mbps")
	}
	// A full one-second average ending by 2s must reach the target, not a single
	// arbitrarily short peak. Evaluate all points where the window count changes.
	ramp := false
	candidates := []time.Duration{time.Second, 2 * time.Second}
	for _, a := range t.Arrivals {
		if a.At >= time.Second && a.At <= 2*time.Second {
			candidates = append(candidates, a.At)
		}
		end := a.At + time.Second
		if end >= time.Second && end <= 2*time.Second {
			candidates = append(candidates, end)
		}
	}
	for _, end := range candidates {
		if bytesIn(end-time.Second, end) >= required(time.Second) {
			ramp = true
			break
		}
	}
	if !ramp {
		failures = append(failures, "100 Mbps not reached within two seconds")
	}
	previous := 2 * time.Second
	for _, a := range t.Arrivals {
		if a.At < previous {
			continue
		}
		if a.At-previous >= 500*time.Millisecond {
			failures = append(failures, "useful-byte stall at least 500 ms after ramp")
			break
		}
		previous = a.At
	}
	if t.Duration-previous >= 500*time.Millisecond {
		failures = append(failures, "useful-byte stall at transfer tail")
	}
	if t.Sustained {
		// Window count changes at each arrival and at each arrival minus window.
		// Include both sides of discrete events, using 1ns resolution of the trace.
		starts := []time.Duration{2 * time.Second, t.Duration - 5*time.Second}
		for _, a := range t.Arrivals {
			for _, p := range []time.Duration{a.At, a.At + time.Nanosecond, a.At - 5*time.Second, a.At - 5*time.Second + time.Nanosecond} {
				if p >= 2*time.Second && p+5*time.Second <= t.Duration {
					starts = append(starts, p)
				}
			}
		}
		for _, start := range starts {
			if bytesIn(start, start+5*time.Second) < required(5*time.Second) {
				failures = append(failures, "rolling five-second useful rate below 100 Mbps")
				break
			}
		}
	}
	return failures
}

type Exchange struct {
	Elapsed time.Duration
	Loaded  bool
	Err     string
	Bytes   int
}

func CheckExchanges(samples []Exchange) []string {
	if len(samples) == 0 {
		return []string{"missing application RTT samples"}
	}
	var out []string
	for i, s := range samples {
		ceiling := 250 * time.Millisecond
		if s.Loaded {
			ceiling = 2 * time.Second
		}
		if s.Err != "" || s.Elapsed <= 0 || s.Elapsed > ceiling || s.Bytes < 1 {
			out = append(out, fmt.Sprintf("exchange %d exceeds application RTT/completion gate", i))
		}
	}
	return out
}
