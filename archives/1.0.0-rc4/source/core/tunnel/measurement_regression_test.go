package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func checkProductionPatternRejectsCrossSessionUserDirectionStream(t *testing.T) {
	expected := "session1/user1/upload/1"
	body := make([]byte, 1025)
	scratch := make([]byte, len(body))
	for _, id := range []string{"session2/user1/upload/1", "session1/user2/upload/1", "session1/user1/download/1", "session1/user1/upload/2"} {
		newProductionPattern(id).fill(body)
		if e := newProductionPattern(expected).verify(body, scratch); e == nil {
			t.Fatalf("swap %s passed", id)
		}
	}
	newProductionPattern(expected).fill(body)
	body[512] ^= 1
	if e := newProductionPattern(expected).verify(body, scratch); e == nil {
		t.Fatal("corruption passed")
	}
	// Offset remains correct across arbitrary packet boundaries.
	newProductionPattern(expected).fill(body)
	verifier := newProductionPattern(expected)
	for _, part := range [][]byte{body[:1], body[1:17], body[17:]} {
		if e := verifier.verify(part, scratch); e != nil {
			t.Fatal(e)
		}
	}
}
func checkProductionReadRejectsEarlyEOFAndCountsOnlyVerifiedBytes(t *testing.T) {
	body := make([]byte, 500)
	newProductionPattern("stream").fill(body)
	var stop atomic.Bool
	var delivered atomic.Int64
	if e := productionReadVerified(bytes.NewReader(body), "stream", &stop, &delivered); !errors.Is(e, io.EOF) {
		t.Fatal(e)
	}
	if delivered.Load() != 500 {
		t.Fatal(delivered.Load())
	}
	delivered.Store(0)
	body[20] ^= 1
	if e := productionReadVerified(bytes.NewReader(body), "stream", &stop, &delivered); e == nil || delivered.Load() != 0 {
		t.Fatal("corrupt data counted", e)
	}
	stop.Store(true)
	if e := productionReadVerified(bytes.NewReader(nil), "stream", &stop, &delivered); e != nil {
		t.Fatal(e)
	}
}

type shortProductionWriter struct{ bytes.Buffer }

func (w *shortProductionWriter) Write(b []byte) (int, error) {
	return w.Buffer.Write(b[:min(3, len(b))])
}
func checkProductionWriteHandlesPartialWrites(t *testing.T) {
	w := &shortProductionWriter{}
	data := []byte("partial write must preserve every byte")
	if e := productionWriteFull(w, data); e != nil || !bytes.Equal(w.Bytes(), data) {
		t.Fatal(e, w.Bytes())
	}
}
func checkProductionNearestRankIncludesTail(t *testing.T) {
	samples := []time.Duration{4, 100, 2, 3, 1}
	if productionPercentile(samples, .95) != 100 || productionPercentile(samples, .99) != 100 || productionPercentile(samples, .5) != 3 {
		t.Fatal("tail excluded")
	}
	if samples[0] != 4 || samples[1] != 100 {
		t.Fatal("quantile mutated input")
	}
}
func TestProductionMeasurementDetectsBlackholeBeforeWatchdog(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go io.Copy(b, b)
	result, e := productionMeasure(context.Background(), productionMeasureOptions{Duration: time.Second, Cadence: 20 * time.Millisecond, ProbeDeadline: 100 * time.Millisecond, MaxGap: 80 * time.Millisecond}, []*productionLoadTarget{{errors: make(chan error, 1)}}, []net.Conn{a}, nil, nil, t.Logf)
	if e == nil || !strings.Contains(e.Error(), "blackhole") || result.Elapsed > 500*time.Millisecond || len(result.Windows) == 0 {
		t.Fatal(result, e)
	}
}
func TestProductionMeasurementSlowUserCannotStopOtherUserOrSampler(t *testing.T) {
	slow, peer := net.Pipe()
	defer slow.Close()
	defer peer.Close()
	fast, echo := net.Pipe()
	defer fast.Close()
	defer echo.Close()
	go io.Copy(echo, echo)
	targets := []*productionLoadTarget{{errors: make(chan error, 1)}, {errors: make(chan error, 1)}}
	result, e := productionMeasure(context.Background(), productionMeasureOptions{Duration: time.Second, Cadence: 20 * time.Millisecond, ProbeDeadline: 100 * time.Millisecond}, targets, []net.Conn{slow, fast}, nil, nil, t.Logf)
	rtts, _, _, _ := productionSampleSummary(result.Samples, 1)
	_, _, timeouts, _ := productionSampleSummary(result.Samples, 0)
	if e == nil || timeouts != 1 || len(rtts) < 2 || len(result.Windows) < 4 || result.Elapsed > 500*time.Millisecond {
		t.Fatal(result, e)
	}
}
func TestProductionMeasurementEndsBeforeDrainAndKeepsResultsOnFailure(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go io.Copy(b, b)
	targets := []*productionLoadTarget{{errors: make(chan error, 1)}}
	result, e := productionMeasure(context.Background(), productionMeasureOptions{Duration: 100 * time.Millisecond, Cadence: 20 * time.Millisecond, ProbeDeadline: 50 * time.Millisecond}, targets, []net.Conn{a}, nil, nil, t.Logf)
	if e != nil || result.Elapsed > 400*time.Millisecond || len(result.Samples) < 4 {
		t.Fatal(result, e)
	}
	// A failure doesn't discard the fixed-window/probe snapshot.
	failures := make(chan error, 1)
	go func() { time.Sleep(60 * time.Millisecond); failures <- io.EOF }()
	result, e = productionMeasure(context.Background(), productionMeasureOptions{Duration: time.Second, Cadence: 20 * time.Millisecond, ProbeDeadline: 50 * time.Millisecond}, targets, []net.Conn{a}, nil, failures, t.Logf)
	if e == nil || len(result.Windows) == 0 || len(result.Samples) == 0 {
		t.Fatal(result, e)
	}
}

// The bandwidth ticker is a measurement clock, not a realtime guarantee.
// On Windows a 20ms ticker may coalesce/drop ticks when the scheduler is busy;
// productionMeasure intentionally reports such gaps instead of inventing data.
// Assert independent cadence and truthful missing-window accounting, not that
// the OS actually scheduled all six tiny intervals.
func TestProductionDiagnosticProbeCadenceKeepsIndependentByteClock(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go io.Copy(b, b)
	result, err := productionMeasure(context.Background(), productionMeasureOptions{Duration: 120 * time.Millisecond, Cadence: 60 * time.Millisecond, BandwidthCadence: 20 * time.Millisecond, ProbeDeadline: 50 * time.Millisecond}, []*productionLoadTarget{{errors: make(chan error, 1)}}, []net.Conn{a}, nil, nil, t.Logf)
	if err != nil || len(result.Samples) != 2 || len(result.Bandwidth) == 0 {
		t.Fatal("independent probe/byte-clock measurement failed", result, err)
	}
	// The fixed 20ms clock must reach the final 120ms boundary. An
	// inadvertently shared 60ms probe ticker would not log period_ms=20.
	if last := result.Bandwidth[len(result.Bandwidth)-1].Index; last != 6 {
		t.Fatal("independent byte clock did not reach its final boundary", result.Bandwidth)
	}
	previous := 0
	for _, window := range result.Bandwidth {
		if window.User != 0 || window.Index <= previous || window.Index > 6 || window.Duration <= 0 {
			t.Fatal("invalid independent byte-clock window", window, result.Bandwidth)
		}
		if window.Index > previous+1 {
			gap := fmt.Sprintf("production_window_missed after_index=%d through_index=%d", previous, window.Index-1)
			found := false
			for _, line := range result.Windows {
				if line == gap {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("unreported gap in independent byte-clock: %s; windows=%v", gap, result.Windows)
			}
		}
		previous = window.Index
	}
	for _, line := range result.Windows {
		if strings.HasPrefix(line, "production_window user=") && !strings.Contains(line, "period_ms=20") {
			t.Fatalf("probe cadence replaced independent byte cadence: %s", line)
		}
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioMeasurementRegression(t *testing.T) {
	t.Log("check=checkProductionPatternRejectsCrossSessionUserDirectionStream")
	checkProductionPatternRejectsCrossSessionUserDirectionStream(t)
	t.Log("check=checkProductionReadRejectsEarlyEOFAndCountsOnlyVerifiedBytes")
	checkProductionReadRejectsEarlyEOFAndCountsOnlyVerifiedBytes(t)
	t.Log("check=checkProductionWriteHandlesPartialWrites")
	checkProductionWriteHandlesPartialWrites(t)
	t.Log("check=checkProductionNearestRankIncludesTail")
	checkProductionNearestRankIncludesTail(t)
}

func TestRev2MeasurementCannotLoseFinalProbeFailureAtWindowBoundary(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	// No echo. The byte interval ends before this probe's own deadline.
	result, err := productionMeasure(context.Background(), productionMeasureOptions{Duration: 40 * time.Millisecond, Cadence: 100 * time.Millisecond, ProbeDeadline: 100 * time.Millisecond}, []*productionLoadTarget{{errors: make(chan error, 1)}}, []net.Conn{a}, nil, nil, t.Logf)
	_, _, timeouts, _ := productionSampleSummary(result.Samples, 0)
	if err == nil || timeouts != 1 || len(result.Samples) != 1 {
		t.Fatal("final probe timeout was recorded but not returned", result, err)
	}
	if result.Elapsed != 40*time.Millisecond {
		t.Fatal("waiting for final probe extended byte interval", result.Elapsed)
	}
}
