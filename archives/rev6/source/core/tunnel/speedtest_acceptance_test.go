package tunnel

import (
	"fmt"
	"math"
	"testing"
	"time"
)

const speedtestAcceptedMbps = 90.0
const speedtestAcceptedMaxRTT = 1100 * time.Millisecond

// A burst must not conceal a dead download at the end.
// impressive initial burst must not conceal a dead download at the end, even
// when the 30s average exceeds a threshold. No zero-traffic probe is ignored.
func speedtestWindowQualification(windows []productionBandwidthWindow, elapsed time.Duration, users int, minimumMbps float64) error {
	if elapsed < 30*time.Second || users < 1 || len(windows) < users*10 {
		return fmt.Errorf("insufficient speedtest measurement windows elapsed=%s windows=%d", elapsed, len(windows))
	}
	// The byte-clock is a one-second cadence. Enforce ten consecutive,
	// concurrent indices at the END of the measured interval, not merely the
	// most recent ten entries for each user: a skipped tick, duplicated tick,
	// or stale user otherwise gives a falsely passing average.
	lastIndex := int(math.Round(elapsed.Seconds()))
	if lastIndex < 10 {
		return fmt.Errorf("insufficient speedtest duration %s", elapsed)
	}
	tail := make([]map[int]productionBandwidthWindow, users)
	for i := range tail {
		tail[i] = make(map[int]productionBandwidthWindow, 10)
	}
	for _, w := range windows {
		if w.Duration <= 0 || w.Upload < 0 || w.Download < 0 || w.User < 0 || w.User >= users || w.Index <= 0 || w.Index > lastIndex {
			return fmt.Errorf("invalid bandwidth window: %+v", w)
		}

		if _, exists := tail[w.User][w.Index]; exists {
			return fmt.Errorf("duplicate speedtest window user=%d index=%d", w.User+1, w.Index)
		}
		tail[w.User][w.Index] = w
	}
	for user := 0; user < users; user++ {
		var up, down int64
		var duration time.Duration
		for index := 1; index <= lastIndex; index++ {
			if _, ok := tail[user][index]; !ok {
				return fmt.Errorf("missing full-window tick user=%d index=%d", user, index)
			}
		}
		for index := lastIndex - 9; index <= lastIndex; index++ {
			w, ok := tail[user][index]
			if !ok {
				return fmt.Errorf("speedtest missing tail window user=%d index=%d", user+1, index)
			}
			up += w.Upload
			down += w.Download
			duration += w.Duration
		}
		if duration < 8*time.Second || duration > 12*time.Second {
			return fmt.Errorf("speedtest sparse or inflated tail user=%d duration=%s", user+1, duration)
		}
		upMbps := float64(up) * 8 / duration.Seconds() / 1e6
		downMbps := float64(down) * 8 / duration.Seconds() / 1e6
		if math.IsNaN(upMbps) || math.IsNaN(downMbps) || upMbps < minimumMbps || downMbps < minimumMbps {
			return fmt.Errorf("user %d speedtest tail upload %.3f download %.3f Mbps below %.1f", user+1, upMbps, downMbps, minimumMbps)
		}
	}
	return nil
}

func TestSpeedtestWindowQualificationRejectsOneSidedCollapse(t *testing.T) {
	var w []productionBandwidthWindow
	for i := 0; i < 30; i++ {
		w = append(w, productionBandwidthWindow{User: 0, Index: i + 1, Duration: time.Second, Upload: 16 << 20, Download: 16 << 20})
	}
	if err := speedtestWindowQualification(w, 30*time.Second, 1, speedtestAcceptedMbps); err != nil {
		t.Fatal(err)
	}
	for i := 20; i < 30; i++ {
		w[i].Download = 0
	}
	if err := speedtestWindowQualification(w, 30*time.Second, 1, speedtestAcceptedMbps); err == nil {
		t.Fatal("collapsed download passed speedtest gate")
	}
	for i := 20; i < 30; i++ {
		w[i].Download = 16 << 20
		w[i].Upload = 0
	}
	if err := speedtestWindowQualification(w, 30*time.Second, 1, speedtestAcceptedMbps); err == nil {
		t.Fatal("collapsed upload passed speedtest gate")
	}
}

// A successful gate must not accept only one direction, or interpret p95
// RTT <1100ms as sufficient when even a single sample exceeds the max.
func speedtestRTTQualification(rtt []time.Duration, timeouts, missed, overlap int) error {
	if len(rtt) == 0 || timeouts != 0 || missed != 0 || overlap < 1 {
		return fmt.Errorf("speedtest probes missing/failing: count=%d timeouts=%d missed=%d overlap=%d", len(rtt), timeouts, missed, overlap)
	}
	for _, sample := range rtt {
		if sample <= 0 || sample >= speedtestAcceptedMaxRTT {
			return fmt.Errorf("speedtest max RTT %s must be < %s", sample, speedtestAcceptedMaxRTT)
		}
	}
	return nil
}

func TestRev4SpeedAndRTTEvidenceRejectsPartialSuccess(t *testing.T) {
	windows := make([]productionBandwidthWindow, 0, 30)
	for i := 1; i <= 30; i++ {
		windows = append(windows, productionBandwidthWindow{User: 0, Index: i, Duration: time.Second, Upload: 11_250_000, Download: 11_250_000})
	}
	if err := speedtestWindowQualification(windows, 30*time.Second, 1, speedtestAcceptedMbps); err != nil {
		t.Fatal(err)
	}
	if err := speedtestRTTQualification([]time.Duration{399 * time.Millisecond, 1099 * time.Millisecond}, 0, 0, 2); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                      string
		samples                   []time.Duration
		timeouts, missed, overlap int
	}{
		{"zero_rtt", []time.Duration{0}, 0, 0, 1},
		{"exact_1100ms", []time.Duration{1100 * time.Millisecond}, 0, 0, 1},
		{"one_outlier", []time.Duration{410 * time.Millisecond, 1101 * time.Millisecond}, 0, 0, 2},
		{"timeout", []time.Duration{410 * time.Millisecond}, 1, 0, 1},
		{"missed", []time.Duration{410 * time.Millisecond}, 0, 1, 1},
		{"no_bulk_overlap", []time.Duration{410 * time.Millisecond}, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := speedtestRTTQualification(tc.samples, tc.timeouts, tc.missed, tc.overlap); err == nil {
				t.Fatal("unsafe RTT falsely accepted")
			}
		})
	}
	for i := 20; i < 30; i++ {
		windows[i].Download = 3_000_000
	}
	if err := speedtestWindowQualification(windows, 30*time.Second, 1, speedtestAcceptedMbps); err == nil {
		t.Fatal("download below 90 Mbps accepted")
	}
	for i := 20; i < 30; i++ {
		windows[i].Download = 4_000_000
		windows[i].Upload = 3_000_000
	}
	if err := speedtestWindowQualification(windows, 30*time.Second, 1, speedtestAcceptedMbps); err == nil {
		t.Fatal("upload below 90 Mbps accepted")
	}
}
