package qualification_test

import (
	"itom.local/core/qualification"
	"testing"
	"time"
)

func timeline(duration time.Duration, rate uint64) qualification.Transfer {
	out := qualification.Transfer{Duration: duration, Sustained: duration == 60*time.Second}
	for at := time.Duration(0); at < duration; at += 10 * time.Millisecond {
		out.Arrivals = append(out.Arrivals, qualification.Arrival{At: at, Bytes: rate / 100})
	}
	return out
}
func TestUsefulReceiverEvidenceMustSustainTheThreshold(t *testing.T) {
	for _, d := range []time.Duration{12 * time.Second, 60 * time.Second} {
		if failures := qualification.CheckTransfer(timeline(d, qualification.RateBytesPerSecond)); len(failures) != 0 {
			t.Fatal(failures)
		}
	}
}
func TestAPeakAbove200MbpsCannotHidePoorMeanOrTail(t *testing.T) {
	trace := timeline(12*time.Second, qualification.RateBytesPerSecond/2)
	for i := range trace.Arrivals {
		if trace.Arrivals[i].At < time.Second {
			trace.Arrivals[i].Bytes *= 5
		}
	}
	if failures := qualification.CheckTransfer(trace); len(failures) == 0 {
		t.Fatal("peak-only result qualified")
	}
}
func TestFastOverallMeanCannotHideHalfSecondStalls(t *testing.T) {
	trace := timeline(12*time.Second, qualification.RateBytesPerSecond*2)
	kept := trace.Arrivals[:0]
	for _, a := range trace.Arrivals {
		if a.At < 5*time.Second || a.At >= 5600*time.Millisecond {
			kept = append(kept, a)
		}
	}
	trace.Arrivals = kept
	if failures := qualification.CheckTransfer(trace); len(failures) == 0 {
		t.Fatal("stall hidden by average")
	}
}
func TestWarmFastTailCannotHideSlowStartup(t *testing.T) {
	trace := timeline(12*time.Second, qualification.RateBytesPerSecond*2)
	for i := range trace.Arrivals {
		if trace.Arrivals[i].At < 3*time.Second {
			trace.Arrivals[i].Bytes = 1
		}
	}
	if failures := qualification.CheckTransfer(trace); len(failures) == 0 {
		t.Fatal("late ramp qualified")
	}
}
func TestAOneMillisecondLowWindowCannotBeHiddenBetweenSampleTicks(t *testing.T) {
	trace := timeline(60*time.Second, qualification.RateBytesPerSecond)
	// Exactly threshold elsewhere; any real missing receiver bytes must fail.
	trace.Arrivals[3101].Bytes--
	if failures := qualification.CheckTransfer(trace); len(failures) == 0 {
		t.Fatal("rolling underdelivery qualified")
	}
}
func TestIdleMaximumCannotBecomeAPercentile(t *testing.T) {
	samples := make([]qualification.Exchange, 1000)
	for i := range samples {
		samples[i] = qualification.Exchange{Elapsed: 190 * time.Millisecond, Bytes: 32}
	}
	samples[999].Elapsed = qualification.LightRTTLimit + time.Nanosecond
	if failures := qualification.CheckExchanges(samples); len(failures) != 1 {
		t.Fatal("idle spike hidden", failures)
	}
	samples[999].Loaded = true
	samples[999].Elapsed = 1900 * time.Millisecond
	if failures := qualification.CheckExchanges(samples); len(failures) != 0 {
		t.Fatal("acceptable loaded spike rejected", failures)
	}
}

func TestTemporaryLightMaximumKeepsPreferredTargetAndLoadedFailuresSeparate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample qualification.Exchange
		pass   bool
	}{
		{"preferred", qualification.Exchange{Elapsed: 250 * time.Millisecond, Bytes: 32}, true},
		{"temporary_boundary", qualification.Exchange{Elapsed: 300 * time.Millisecond, Bytes: 32}, true},
		{"light_spike", qualification.Exchange{Elapsed: 300*time.Millisecond + time.Nanosecond, Bytes: 32}, false},
		{"loaded_boundary", qualification.Exchange{Elapsed: 2 * time.Second, Bytes: 32, Loaded: true}, true},
		{"loaded_spike", qualification.Exchange{Elapsed: 2*time.Second + time.Nanosecond, Bytes: 32, Loaded: true}, false},
		{"failed_completion", qualification.Exchange{Elapsed: 290 * time.Millisecond, Bytes: 32, Err: "failed"}, false},
		{"missing_payload", qualification.Exchange{Elapsed: 290 * time.Millisecond}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(qualification.CheckExchanges([]qualification.Exchange{tc.sample})) == 0; got != tc.pass {
				t.Fatal("temporary maximum changed completion or loaded policy", tc.sample, got)
			}
		})
	}
	if qualification.PreferredLightRTT != 250*time.Millisecond {
		t.Fatal("preferred target disappeared")
	}
}
func TestMissingOrOutOfOrderRawEvidenceFails(t *testing.T) {
	if len(qualification.CheckTransfer(qualification.Transfer{Duration: 12 * time.Second})) == 0 {
		t.Fatal("missing trace passed")
	}
	trace := timeline(12*time.Second, qualification.RateBytesPerSecond)
	trace.Arrivals[2].At = 0
	if len(qualification.CheckTransfer(trace)) == 0 {
		t.Fatal("corrupt trace passed")
	}
}
