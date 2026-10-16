package tunnel

import (
	"math"
	"os"
	"testing"
	"time"
)

func strictSpeedGate(rate, minimum float64) bool {
	return minimum > 0 && !math.IsNaN(minimum) && !math.IsInf(minimum, 0) && !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate >= minimum
}

func TestRev8StrictSpeedFloorHasNoTolerance(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		want bool
	}{
		{90, false}, {99.999, false}, {math.Nextafter(100, 0), false},
		{100, true}, {100.001, true}, {math.NaN(), false}, {math.Inf(1), false},
	} {
		if strictSpeedGate(tc.rate, 100) != tc.want {
			t.Fatal(tc)
		}
	}
}

// Qualification uses the minimum verified rate across duplex directions and
// the sustained tail. Compare actual durations: 600 ms is inclusive, whereas
// 700/800 ms are strict; truncating to integer milliseconds changes the rule.
func productionPerformanceTier(minMbps float64, maxRTT time.Duration) (string, bool) {
	return performanceTierWithTolerance(minMbps, maxRTT, 0)
}

// Historical tier comparison is diagnostic only; default tolerance is zero.
// It never changes transport limits, deadlines, proof or payload validation.
func qualificationPerformanceTier(t *testing.T, minMbps float64, maxRTT time.Duration) (string, bool) {
	t.Helper()
	allowance := performanceAllowance(t)
	nominal, nominalOK := productionPerformanceTier(minMbps, maxRTT)
	t.Logf("performance_allowance percent=%d nominal_tier=%s nominal_accepted=%t", allowance, nominal, nominalOK)
	return performanceTierWithTolerance(minMbps, maxRTT, allowance)
}

func performanceAllowance(t *testing.T) int {
	t.Helper()
	allowance := 0
	switch os.Getenv("ITOM_LOAD_TOLERANCE_PERCENT") {
	case "", "0":
	case "10":
		allowance = 10
	default:
		t.Fatal("ITOM_LOAD_TOLERANCE_PERCENT must be 0 or 10")
	}
	return allowance
}

func TestHistoricalTierDiagnosticsHaveNoDefaultAllowance(t *testing.T) {
	t.Setenv("ITOM_LOAD_TOLERANCE_PERCENT", "")
	if _, ok := qualificationPerformanceTier(t, 27, 600*time.Millisecond); ok {
		t.Fatal("obsolete 10% tolerance remained the default")
	}
}

func performanceTierWithTolerance(minMbps float64, maxRTT time.Duration, allowance int) (string, bool) {
	if allowance != 0 && allowance != 10 {
		return "", false
	}
	if math.IsNaN(minMbps) || math.IsInf(minMbps, 0) || maxRTT < 0 {
		return "", false
	}
	switch {
	case minMbps >= float64(100*(100-allowance))/100 && maxRTT < time.Duration(800*(100+allowance))*time.Millisecond/100:
		return "100_mbps_lt800ms", true
	case minMbps >= float64(50*(100-allowance))/100 && maxRTT < time.Duration(700*(100+allowance))*time.Millisecond/100:
		return "50_mbps_lt700ms", true
	case minMbps >= float64(30*(100-allowance))/100 && maxRTT <= time.Duration(600*(100+allowance))*time.Millisecond/100:
		return "30_mbps_le600ms", true
	default:
		return "", false
	}
}

func checkPerformanceToleranceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		rate     float64
		rtt      time.Duration
		accepted bool
	}{
		{27, 660 * time.Millisecond, true},
		{math.Nextafter(27, 0), 600 * time.Millisecond, false},
		{27, 660*time.Millisecond + time.Nanosecond, false},
		{45, 770*time.Millisecond - time.Nanosecond, true},
		{45, 770 * time.Millisecond, false},
		{math.Nextafter(45, 0), 700 * time.Millisecond, false},
		{90, 880*time.Millisecond - time.Nanosecond, true},
		{90, 880 * time.Millisecond, false},
		{math.Nextafter(90, 0), 800 * time.Millisecond, false},
		{math.NaN(), 0, false}, {math.Inf(1), 0, false}, {90, -1, false},
	} {
		_, accepted := performanceTierWithTolerance(tc.rate, tc.rtt, 10)
		if accepted != tc.accepted {
			t.Fatal(tc, accepted)
		}
	}
	if _, ok := performanceTierWithTolerance(100, 0, 11); ok {
		t.Fatal("unsupported allowance")
	}
}

func checkProductionPerformanceTierBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		mbps float64
		rtt  time.Duration
		tier string
	}{
		{"30-inclusive", 30, 600 * time.Millisecond, "30_mbps_le600ms"},
		{"30-rtt-fraction", 30, 600*time.Millisecond + time.Nanosecond, ""},
		{"30-rate-low", math.Nextafter(30, 0), 500 * time.Millisecond, ""},
		{"50-strict-pass", 50, 700*time.Millisecond - time.Nanosecond, "50_mbps_lt700ms"},
		{"50-strict-fail", 50, 700 * time.Millisecond, ""},
		{"50-rate-low", math.Nextafter(50, 0), 650 * time.Millisecond, ""},
		{"100-strict-pass", 100, 800*time.Millisecond - time.Nanosecond, "100_mbps_lt800ms"},
		{"100-strict-fail", 100, 800 * time.Millisecond, ""},
		{"100-rate-low", math.Nextafter(100, 0), 750 * time.Millisecond, ""},
		{"100-alternative", 100, 700 * time.Millisecond, "100_mbps_lt800ms"},
		{"strongest-tier", 120, 500 * time.Millisecond, "100_mbps_lt800ms"},
		{"nan", math.NaN(), 0, ""},
		{"infinite", math.Inf(1), 0, ""},
		{"negative-rtt", 100, -time.Nanosecond, ""},
	} {
		checkScenarioBoundary(t, tc.name, func(t *testing.T) {
			tier, ok := productionPerformanceTier(tc.mbps, tc.rtt)
			if tier != tc.tier || ok != (tc.tier != "") {
				t.Fatal(tier, ok, tc)
			}
		})
	}
}

func checkProductionPerformanceRequiresBothDirectionsAndTail(t *testing.T) {
	for _, rates := range [][4]float64{{120, 29, 120, 120}, {120, 120, 29, 120}, {120, 120, 120, 29}} {
		if _, ok := productionPerformanceTier(min(rates[0], rates[1], rates[2], rates[3]), 500*time.Millisecond); ok {
			t.Fatal("aggregate or initial burst hid weak duplex/tail rate", rates)
		}
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioPerformanceAcceptance(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		rtt  time.Duration
		want bool
	}{
		{100, time.Second - time.Nanosecond, true},
		{90, time.Second - time.Nanosecond, true},
		{math.Nextafter(90, 0), 500 * time.Millisecond, false},
		{150, time.Second, false}, {150, time.Second + time.Nanosecond, false},
		{100, 0, false}, {math.NaN(), time.Millisecond, false},
		{math.Inf(1), time.Millisecond, false},
	} {
		if got := temporaryDuplexPerformanceGate(tc.rate, tc.rtt); got != tc.want {
			t.Fatal("temporary 100/<1000 gate", tc, got)
		}
	}

	for _, tc := range []struct {
		rate float64
		rtt  time.Duration
		want bool
	}{
		{75, time.Second - time.Nanosecond, true}, {math.Nextafter(75, 0), 500 * time.Millisecond, false},
		{67.5, 100 * time.Millisecond, false},
		{100, time.Second, false}, {math.NaN(), time.Millisecond, false}, {math.Inf(1), time.Millisecond, false},
	} {
		if got := rttPriorityPerformanceGate(tc.rate, tc.rtt); got != tc.want {
			t.Fatal("RTT priority hard75Mbps/<1000", tc, got)
		}
	}

	t.Log("check=checkPerformanceToleranceBoundaries")
	checkPerformanceToleranceBoundaries(t)
	t.Log("check=checkProductionPerformanceTierBoundaries")
	checkProductionPerformanceTierBoundaries(t)
	t.Log("check=checkProductionPerformanceRequiresBothDirectionsAndTail")
	checkProductionPerformanceRequiresBothDirectionsAndTail(t)
}

// Temporary 100 Mbps nominal target with the user-authorized 10% speed tolerance
// and strict <1s RTT. A synthetic PASS only evaluates these
// numerical bounds; it never authenticates a field result or permits release.
func temporaryDuplexPerformanceGate(minMbps float64, maxRTT time.Duration) bool {
	return stableGoalSpeed(minMbps, 100) && maxRTT > 0 && maxRTT < time.Second
}

// Latest user requirement is a hard 75Mbps floor regardless of RTT improvement.
// The 10percent allowance applies to the advisory 100Mbps target, not this floor.
func rttPriorityPerformanceGate(minMbps float64, maxRTT time.Duration) bool {
	return !math.IsNaN(minMbps) && !math.IsInf(minMbps, 0) && minMbps >= 75 && maxRTT > 0 && maxRTT < time.Second
}
