package tunnel

import (
	"math"
	"os"
	"testing"
	"time"
)

// Useful duplex Mbps, never MIME/provider cap or upload+download sum.
// The absolute 1000ms cap does not receive the general 10% allowance.
func mobileAdaptiveRTTLimit(baseline time.Duration, usefulMbps float64) (time.Duration, bool) {
	if baseline <= 0 || baseline > time.Second || usefulMbps < 0 || math.IsNaN(usefulMbps) || math.IsInf(usefulMbps, 0) {
		return 0, false
	}
	overhead := math.Max(150, 3*usefulMbps)
	baseMs := float64(baseline) / float64(time.Millisecond)
	// A cold IDLE mailbox must notify the client and then return FETCH data.
	// Budget two propagation RTTs even with DONE/FETCH overlap, plus the
	// user-selected 150ms processing margin. Do not inflate unloaded baseline.
	millis := math.Max(baseMs+overhead, 2*baseMs+150) * 1.1
	if millis >= 1000 {
		return time.Second, true
	}
	return time.Duration(millis * float64(time.Millisecond)), true
}

// Temporary user acceptance: every zero-loss probe must be strictly below 1s.
// Keep the former adaptive budget as a diagnostic, not an additional gate.
func mobileRTTLimit(baseline time.Duration, usefulMbps float64) (time.Duration, bool) {
	if _, valid := mobileAdaptiveRTTLimit(baseline, usefulMbps); !valid {
		return 0, false
	}
	return time.Second, true
}

func mobileRTTAccepted(rtt, limit time.Duration, loss float64) bool {
	return rtt > 0 && (loss > 0 && rtt <= limit || loss == 0 && rtt < limit)
}

func mobileSpeedFloor(working, total int) (float64, bool) {
	if working < 1 || total < 1 || working > total {
		return 0, false
	}
	if working == total {
		return 54, true
	}
	if working*2 >= total {
		return 27, true
	}
	return 0, false // Below half is outside the current qualification scope.
}

// A loss profile may need several ordered retransmissions across IMAP turns.
// This is a hard 3s bound, not an additional tolerance or a speed-derived cap.
func mobileProfileLimits(baseline time.Duration, usefulMbps, loss float64, working, total int) (float64, time.Duration, bool) {
	floor, valid := mobileSpeedFloor(working, total)
	if !valid || baseline <= 0 || baseline > 3*time.Second || usefulMbps < 0 || math.IsNaN(usefulMbps) || math.IsInf(usefulMbps, 0) || math.IsNaN(loss) || math.IsInf(loss, 0) || loss < 0 || loss > 5 {
		return 0, 0, false
	}
	if loss > 0 {
		return 25 * .9, 3 * time.Second, true
	}
	limit, latencyValid := mobileRTTLimit(baseline, usefulMbps)
	if !latencyValid {
		return 0, 0, false
	}

	return floor, limit, true
}

// Split the independent byte-clock into aligned 5s windows. Every probe uses
// its own window, so a faster interval cannot conceal slow/high-RTT intervals.
func mobileWindowRate(result productionMeasureResult, user, probeIndex int) (float64, bool) {
	if probeIndex <= 0 {
		return 0, false
	}
	first := ((probeIndex-1)/5)*5 + 1
	last := first + 4
	var up, down int64
	var duration time.Duration
	count := 0
	seen := map[int]bool{}
	for _, w := range result.Bandwidth {
		if w.User != user || w.Index < first || w.Index > last {
			continue
		}
		if seen[w.Index] || w.Duration <= 0 || w.Upload < 0 || w.Download < 0 {
			return 0, false
		}
		seen[w.Index] = true
		count++
		up += w.Upload
		down += w.Download
		duration += w.Duration
	}
	if count != 5 {
		return 0, false
	}
	return float64(min(up, down)) * 8 / duration.Seconds() / 1e6, true
}

type mobileAcceptance struct {
	SpeedFloor, MinimumMbps         float64
	SpeedApplicable, SpeedOK, RTTOK bool
	RTTFailures, Samples            int
	MaxRTT                          time.Duration
}

func mobileProbeWindowRate(result productionMeasureResult, sample productionProbeSample) (float64, bool) {
	if sample.Scheduled < 0 || sample.Index <= 0 {
		return 0, false
	}
	return mobileWindowRate(result, sample.User, int(sample.Scheduled/time.Second)+1)
}

func evaluateMobileLoad(t *testing.T, result productionMeasureResult, baseline time.Duration, working, total int, mobileCap float64) mobileAcceptance {
	t.Helper()
	loss := loadEnvFloat(t, "ITOM_LOAD_MOBILE_LOSS_PERCENT", 0)
	floor, _, valid := mobileProfileLimits(baseline, 0, loss, working, total)
	if !valid || result.Elapsed <= 0 || len(result.Upload) != 1 || len(result.Download) != 1 {
		t.Fatal("invalid mobile qualification input")
	}
	minimum := float64(min(result.Upload[0], result.Download[0])) * 8 / result.Elapsed.Seconds() / 1e6
	if result.Elapsed >= 60*time.Second {
		seconds := int(math.Round(result.Elapsed.Seconds()))
		up, down, _, complete := productionTailBandwidth(result.Bandwidth, seconds, min(60, seconds/2))
		if !complete {
			t.Fatal("mobile sustained tail incomplete")
		}
		minimum = min(minimum, up, down)
	}
	// Relax an absolute speed floor only where an optimistic protocol/wire
	// upper bound already makes its nominal value impossible. Such cases still
	// need progress/integrity and paired field4 non-regression, not a free PASS.
	owners := loadEnvInt(t, "ITOM_LOAD_STREAMS", 4, 1, 8)
	ceiling, ok := mobileCapacityCeiling(mobileCap, baseline, working, owners, os.Getenv("ITOM_LOAD_LITERAL_PLUS") == "1")
	if !ok {
		t.Fatal("invalid protocol capacity input")
	}
	applicable := floor > 0 && ceiling.Duplex >= floor/0.9
	t.Logf("mobile_capacity wire_upper_mbps=%.6f upload_upper_mbps=%.6f duplex_upper_mbps=%.6f writers=%d append_round_trips=%d optimistic=true", ceiling.Wire, ceiling.Upload, ceiling.Duplex, ceiling.Writers, ceiling.AppendTurns)
	a := mobileAcceptance{SpeedFloor: floor, MinimumMbps: minimum, SpeedApplicable: applicable, SpeedOK: !applicable || minimum >= floor, RTTOK: true}
	for _, sample := range result.Samples {
		if sample.User != 0 {
			continue
		}
		if sample.Status != "ok" || sample.Err != nil || sample.RTT <= 0 {
			t.Fatal("mobile probe integrity/cadence", sample)
		}
		// Diagnostic cadence may be longer than one second. Associate RTT
		// with its scheduled byte-clock interval, never its ordinal index.
		rate, complete := mobileProbeWindowRate(result, sample)
		if !complete {
			t.Fatal("missing mobile rate window", sample.Index)
		}
		_, limit, ok := mobileProfileLimits(baseline, rate, loss, working, total)
		if !ok {
			t.Fatal("invalid mobile latency input")
		}
		accepted := mobileRTTAccepted(sample.RTT, limit, loss)
		a.Samples++
		a.MaxRTT = max(a.MaxRTT, sample.RTT)
		if !accepted {
			a.RTTFailures++
			a.RTTOK = false
		}
		t.Logf("mobile_rtt index=%d baseline_ns=%d current_duplex_mbps=%.6f rtt_ns=%d limit_ns=%d accepted=%t", sample.Index, baseline.Nanoseconds(), rate, sample.RTT.Nanoseconds(), limit.Nanoseconds(), accepted)
	}
	t.Logf("mobile_acceptance working=%d total=%d baseline_ns=%d wire_cap_mbps=%.3f minimum_verified_mbps=%.6f floor_mbps=%.3f speed_applicable=%t speed_accepted=%t rtt_accepted=%t rtt_failures=%d samples=%d max_rtt_ns=%d tolerance_percent=10 loss_percent=%.3f nominal_floor_mbps=%.3f hard_cap_ms=%d", working, total, baseline.Nanoseconds(), mobileCap, minimum, floor, applicable, a.SpeedOK, a.RTTOK, a.RTTFailures, a.Samples, a.MaxRTT.Nanoseconds(), loss, floor/.9, func() int {
		if loss > 0 {
			return 3000
		}
		return 1000
	}())
	return a
}

func checkMobileAcceptanceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		baseline time.Duration
		rate     float64
		want     time.Duration
		valid    bool
	}{
		{200 * time.Millisecond, 150, 715 * time.Millisecond, true},
		{200 * time.Millisecond, 30, 605 * time.Millisecond, true},
		{300 * time.Millisecond, 30, 825 * time.Millisecond, true},
		{300 * time.Millisecond, 600, time.Second, true},
		{0, 100, 0, false}, {-1, 100, 0, false}, {200 * time.Millisecond, math.NaN(), 0, false}, {200 * time.Millisecond, math.Inf(1), 0, false},
	} {
		got, ok := mobileAdaptiveRTTLimit(tc.baseline, tc.rate)
		if got != tc.want || ok != tc.valid {
			t.Fatal(tc, got, ok)
		}
	}
	for _, tc := range []struct {
		working, total int
		floor          float64
		valid          bool
	}{{10, 10, 54, true}, {5, 10, 27, true}, {9, 10, 27, true}, {1, 10, 0, false}, {0, 10, 0, false}, {11, 10, 0, false}} {
		got, ok := mobileSpeedFloor(tc.working, tc.total)
		if got != tc.floor || ok != tc.valid {
			t.Fatal(tc, got, ok)
		}
	}
}

func checkMobileRateCannotBorrowFasterWindowOrDirection(t *testing.T) {
	r := productionMeasureResult{}
	for i := 1; i <= 10; i++ {
		up, down := int64(25e6), int64(25e6)
		if i > 5 {
			down = 125e4
		}
		r.Bandwidth = append(r.Bandwidth, productionBandwidthWindow{User: 0, Index: i, Duration: time.Second, Upload: up, Download: down})
	}
	if rate, ok := mobileWindowRate(r, 0, 8); !ok || rate != 10 {
		t.Fatal(rate, ok)
	}
	if rate, ok := mobileProbeWindowRate(r, productionProbeSample{User: 0, Index: 4, Scheduled: 6 * time.Second}); !ok || rate != 10 {
		t.Fatal("diagnostic cadence borrowed the earlier faster window", rate, ok)
	}
	r.Bandwidth = append(r.Bandwidth, r.Bandwidth[8])
	if _, ok := mobileWindowRate(r, 0, 8); ok {
		t.Fatal("duplicate window accepted")
	}
}

func checkMobileLossAcceptanceIsScopedAndBounded(t *testing.T) {
	for _, tc := range []struct {
		loss    float64
		working int
		floor   float64
		cap     time.Duration
		valid   bool
	}{
		{0, 10, 54, time.Second, true}, {0, 5, 27, time.Second, true},
		{.01, 10, 22.5, 3 * time.Second, true}, {5, 5, 22.5, 3 * time.Second, true},
		{5, 4, 0, 0, false}, {-1, 10, 0, 0, false}, {5.01, 10, 0, 0, false}, {math.NaN(), 10, 0, 0, false},
	} {
		floor, cap, valid := mobileProfileLimits(200*time.Millisecond, 30, tc.loss, tc.working, 10)
		if floor != tc.floor || cap != tc.cap || valid != tc.valid {
			t.Fatal(tc, floor, cap, valid)
		}
	}
}

func checkTemporaryZeroLossRTTIsStrictlyBelowOneSecond(t *testing.T) {
	limit, valid := mobileRTTLimit(250*time.Millisecond, 100)
	if !valid || limit != time.Second {
		t.Fatal(limit, valid)
	}
	for _, tc := range []struct {
		rtt      time.Duration
		accepted bool
	}{
		{0, false}, {999 * time.Millisecond, true}, {time.Second, false}, {1001 * time.Millisecond, false},
	} {
		if mobileRTTAccepted(tc.rtt, limit, 0) != tc.accepted {
			t.Fatal(tc)
		}
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioMobileAcceptance(t *testing.T) {
	t.Log("check=checkMobileAcceptanceBoundaries")
	checkMobileAcceptanceBoundaries(t)
	t.Log("check=checkMobileRateCannotBorrowFasterWindowOrDirection")
	checkMobileRateCannotBorrowFasterWindowOrDirection(t)
	t.Log("check=checkMobileLossAcceptanceIsScopedAndBounded")
	checkMobileLossAcceptanceIsScopedAndBounded(t)
	t.Log("check=checkTemporaryZeroLossRTTIsStrictlyBelowOneSecond")
	checkTemporaryZeroLossRTTIsStrictlyBelowOneSecond(t)
}
