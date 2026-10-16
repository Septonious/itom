package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type productionProbeSample struct {
	User, Index int
	Scheduled   time.Duration
	RTT         time.Duration
	Status      string
	Err         error
	Overlap     bool
}
type productionBandwidthWindow struct {
	User, Index      int
	Duration         time.Duration
	Upload, Download int64
}
type productionMeasureResult struct {
	Elapsed          time.Duration
	Upload, Download []int64
	Samples          []productionProbeSample
	Windows          []string                    // bounded last 128 events
	Bandwidth        []productionBandwidthWindow // bounded independent byte-clock windows
}
type productionMeasureOptions struct {
	Duration, Cadence, ProbeDeadline, MaxGap time.Duration
	BandwidthCadence                         time.Duration
	ProbeOffset                              time.Duration
}

// Measurement owns a fixed clock; each user's probe owns a separate worker.
// Slow probes cannot delay byte sampling, another user, or measurement end.
func productionMeasure(parent context.Context, o productionMeasureOptions, targets []*productionLoadTarget, probes []net.Conn, runtimeErrors, workerErrors <-chan error, logf func(string, ...any)) (result productionMeasureResult, failure error) {
	if o.Cadence <= 0 || o.ProbeOffset < 0 || o.ProbeOffset >= o.Cadence {
		return result, fmt.Errorf("invalid probe cadence/offset")
	}
	bandwidthCadence := o.BandwidthCadence
	if bandwidthCadence <= 0 {
		bandwidthCadence = o.Cadence
	}
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	events := make(chan productionProbeSample, len(probes)*4)
	defer func() {
		if failure == nil {
			wg.Wait()
		}
		cancel()
		for _, p := range probes {
			_ = p.SetDeadline(time.Now())
		}
		wg.Wait()
		for {
			select {
			case sample := <-events:
				result.Samples = append(result.Samples, sample)
				logf("production_probe index=%d user=%d scheduled_ms=%d rtt_ms=%d status=%s overlap=%t final_drain=true", sample.Index, sample.User+1, sample.Scheduled.Milliseconds(), sample.RTT.Milliseconds(), sample.Status, sample.Overlap)
				if failure == nil && sample.Err != nil {
					failure = fmt.Errorf("final probe user=%d index=%d status=%s: %w", sample.User+1, sample.Index, sample.Status, sample.Err)
				}
			default:
				return
			}
		}
	}()
	start := time.Now()
	clocks := make([]*applicationByteClock, len(targets))
	for i, t := range targets {
		clocks[i] = newApplicationByteClock(start, o.Duration, bandwidthCadence)
		t.measurement.Store(clocks[i])
	}
	defer func() {
		for i, t := range targets {
			t.measurement.CompareAndSwap(clocks[i], nil)
		}
	}()
	beginUp, beginDown := make([]int64, len(targets)), make([]int64, len(targets))
	previousUp, previousDown := make([]int64, len(targets)), make([]int64, len(targets))
	gapUp, gapDown := make([]time.Time, len(targets)), make([]time.Time, len(targets))
	for i, t := range targets {
		beginUp[i] = t.uploadDelivered.Load()
		beginDown[i] = t.downloadDelivered.Load()
		gapUp[i] = start
		gapDown[i] = start
	}
	for user, p := range probes {
		wg.Add(1)
		go func(user int, p net.Conn) {
			defer wg.Done()
			lastUp, lastDown := beginUp[user], beginDown[user]
			for index := 0; ; index++ {
				scheduled := start.Add(o.ProbeOffset + time.Duration(index)*o.Cadence)
				if !scheduled.Before(start.Add(o.Duration)) {
					return
				}
				if wait := time.Until(scheduled); wait > 0 {
					if pause(ctx, wait) != nil {
						return
					}
				}
				if ctx.Err() != nil {
					return
				}
				sample := productionProbeSample{User: user, Index: index + 1, Scheduled: scheduled.Sub(start)}
				if time.Since(scheduled) >= o.Cadence {
					sample.Status = "missed"
					sample.Err = fmt.Errorf("probe missed cadence")
				} else {
					stamp := time.Now()
					deadline := stamp.Add(o.ProbeDeadline)
					_ = p.SetDeadline(deadline)
					payload := []byte(fmt.Sprintf("probe/%d/%d", user, index))
					received := make([]byte, len(payload))
					sample.Err = productionWriteFull(p, payload)
					if sample.Err == nil {
						_, sample.Err = io.ReadFull(p, received)
					}
					sample.RTT = time.Since(stamp)
					sample.Status = "ok"
					if sample.Err != nil {
						sample.Status = "error"
						if n, ok := sample.Err.(net.Error); ok && n.Timeout() {
							sample.Status = "timeout"
						}
					}
					if sample.Err == nil && !bytes.Equal(payload, received) {
						sample.Err = fmt.Errorf("corrupt application probe")
						sample.Status = "corrupt"
					}
				}
				up, down := targets[user].uploadDelivered.Load(), targets[user].downloadDelivered.Load()
				sample.Overlap = up > lastUp && down > lastDown
				lastUp, lastDown = up, down
				select {
				case events <- sample:
				case <-ctx.Done():
					return
				}
				if sample.Err != nil {
					return
				} // do not resynchronize a partially consumed echo
			}
		}(user, p)
	}
	addWindow := func(format string, args ...any) {
		event := fmt.Sprintf(format, args...)
		logf("%s", event)
		result.Windows = append(result.Windows, event)
		if len(result.Windows) > 128 {
			result.Windows = append([]string(nil), result.Windows[len(result.Windows)-128:]...)
		}
	}
	capture := func(at time.Time) {
		result.Elapsed = min(at.Sub(start), o.Duration)
		result.Upload = make([]int64, len(targets))
		result.Download = make([]int64, len(targets))
		for i, t := range targets {
			if t.identity != "" {
				result.Upload[i], result.Download[i] = clocks[i].totals(len(clocks[i].up))
			} else {
				result.Upload[i] = t.uploadDelivered.Load() - beginUp[i]
				result.Download[i] = t.downloadDelivered.Load() - beginDown[i]
			}
		}
	}
	defer func() { capture(time.Now()) }()
	timer := time.NewTimer(o.Duration)
	defer timer.Stop()
	ticker := time.NewTicker(bandwidthCadence)
	defer ticker.Stop()
	tick := 0
	lastAt := start
	fiveUp, fiveDown := make([]int64, len(targets)), make([]int64, len(targets))
	fiveAt := start
	sampleWindow := func(scheduled time.Time) error {
		index := int((scheduled.Sub(start) + bandwidthCadence/2) / bandwidthCadence)
		if index <= tick {
			return nil
		}
		if index > tick+1 {
			addWindow("production_window_missed after_index=%d through_index=%d", tick, index-1)
		}
		tick = index
		now := time.Now()
		interval := now.Sub(lastAt)
		if len(targets) > 0 && targets[0].identity != "" {
			interval = bandwidthCadence
		}
		for i, t := range targets {
			up, down := t.uploadDelivered.Load()-beginUp[i], t.downloadDelivered.Load()-beginDown[i]
			if t.identity != "" {
				up, down = clocks[i].totals(index)
			}
			if up > previousUp[i] {
				gapUp[i] = now
			}
			if down > previousDown[i] {
				gapDown[i] = now
			}
			result.Bandwidth = append(result.Bandwidth, productionBandwidthWindow{User: i, Index: tick, Duration: interval, Upload: up - previousUp[i], Download: down - previousDown[i]})
			if len(result.Bandwidth) > 512 {
				result.Bandwidth = append([]productionBandwidthWindow(nil), result.Bandwidth[len(result.Bandwidth)-512:]...)
			}
			addWindow("production_window user=%d index=%d period_ms=%d boundary_ms=%d scheduling_lag_ms=%d observed_ms=%d upload_bytes=%d download_bytes=%d upload_mbps=%.3f download_mbps=%.3f upload_gap_ms=%d download_gap_ms=%d", i+1, tick, bandwidthCadence.Milliseconds(), scheduled.Sub(start).Milliseconds(), now.Sub(scheduled).Milliseconds(), interval.Milliseconds(), up-previousUp[i], down-previousDown[i], float64((up-previousUp[i])*8)/interval.Seconds()/1e6, float64((down-previousDown[i])*8)/interval.Seconds()/1e6, now.Sub(gapUp[i]).Milliseconds(), now.Sub(gapDown[i]).Milliseconds())
			if tick%5 == 0 {
				addWindow("production_window5 user=%d index=%d observed_ms=%d upload_mbps=%.3f download_mbps=%.3f", i+1, tick/5, now.Sub(fiveAt).Milliseconds(), float64((up-fiveUp[i])*8)/now.Sub(fiveAt).Seconds()/1e6, float64((down-fiveDown[i])*8)/now.Sub(fiveAt).Seconds()/1e6)
				fiveUp[i], fiveDown[i] = up, down
			}
			previousUp[i], previousDown[i] = up, down
			if err := t.firstError(); err != nil {
				return fmt.Errorf("target user=%d: %w", i+1, err)
			}
			if o.MaxGap > 0 && (now.Sub(gapUp[i]) >= o.MaxGap || now.Sub(gapDown[i]) >= o.MaxGap) {
				return fmt.Errorf("useful progress blackhole user=%d upload_gap=%s download_gap=%s", i+1, now.Sub(gapUp[i]), now.Sub(gapDown[i]))
			}
		}
		lastAt = now
		if tick%5 == 0 {
			fiveAt = now
		}
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case err := <-runtimeErrors:
			return result, fmt.Errorf("carrier exit during measurement: %w", err)
		case err := <-workerErrors:
			return result, fmt.Errorf("application worker during measurement: %w", err)
		case sample := <-events:
			result.Samples = append(result.Samples, sample)
			logf("production_probe index=%d user=%d scheduled_ms=%d rtt_ms=%d status=%s overlap=%t", sample.Index, sample.User+1, sample.Scheduled.Milliseconds(), sample.RTT.Milliseconds(), sample.Status, sample.Overlap)
			if sample.Err != nil {
				return result, fmt.Errorf("probe user=%d index=%d status=%s: %w", sample.User+1, sample.Index, sample.Status, sample.Err)
			}
		case <-timer.C:
			if o.Duration%bandwidthCadence == 0 {
				if err := sampleWindow(start.Add(o.Duration)); err != nil {
					return result, err
				}
			}
			return result, nil
		case scheduled := <-ticker.C:
			if err := sampleWindow(scheduled); err != nil {
				return result, err
			}
		}
	}
}

func productionSampleSummary(samples []productionProbeSample, user int) (rtts []time.Duration, overlap, timeouts, missed int) {
	for _, s := range samples {
		if user >= 0 && s.User != user {
			continue
		}
		if s.Status == "timeout" {
			timeouts++
		}
		if s.Status == "missed" {
			missed++
		}
		if s.Status == "ok" {
			rtts = append(rtts, s.RTT)
			if s.Overlap {
				overlap++
			}
		}
	}
	sort.Slice(rtts, func(i, j int) bool { return rtts[i] < rtts[j] })
	return
}

func productionReadVerified(reader io.Reader, identity string, stop *atomic.Bool, delivered *atomic.Int64) error {
	return productionReadVerifiedObserver(reader, identity, stop, delivered, nil)
}
func productionReadVerifiedObserver(reader io.Reader, identity string, stop *atomic.Bool, delivered *atomic.Int64, observe func(int)) error {
	generator := newProductionPattern(identity)
	buf := make([]byte, 64<<10)
	scratch := make([]byte, len(buf))
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if e := generator.verify(buf[:n], scratch); e != nil {
				return e
			}
			delivered.Add(int64(n))
			if observe != nil {
				observe(n)
			}
		}
		if err != nil {
			if stop.Load() {
				return nil
			}
			return fmt.Errorf("unexpected active-stream termination: %w", err)
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

// Tail windows use fixed indices and observed durations. Scheduler lag at the
// first boundary can shorten the measured tail without dropping a window.
func productionTailBandwidth(windows []productionBandwidthWindow, last, span int) (up, down float64, elapsed time.Duration, complete bool) {
	seen := map[int]bool{}
	var upload, download int64
	duplicate := false
	for _, w := range windows {
		if w.User != 0 || w.Index <= last-span || w.Index > last {
			continue
		}
		if seen[w.Index] || w.Duration <= 0 {
			duplicate = true
			continue
		}
		seen[w.Index] = true
		elapsed += w.Duration
		upload += w.Upload
		download += w.Download
	}
	if elapsed > 0 {
		up = float64(upload*8) / elapsed.Seconds() / 1e6
		down = float64(download*8) / elapsed.Seconds() / 1e6
	}
	complete = span > 0 && !duplicate && len(seen) == span && elapsed >= time.Duration(span-1)*time.Second && elapsed <= time.Duration(span+1)*time.Second
	return
}

func TestProductionTailAcceptsBoundaryLagButRequiresEveryWindow(t *testing.T) {
	windows := []productionBandwidthWindow{
		{User: 0, Index: 1, Duration: 900 * time.Millisecond, Upload: 2250000, Download: 2250000},
		{User: 0, Index: 2, Duration: time.Second, Upload: 2500000, Download: 2500000},
		{User: 0, Index: 3, Duration: time.Second, Upload: 2500000, Download: 2500000},
	}
	up, down, elapsed, complete := productionTailBandwidth(windows, 3, 3)
	if !complete || elapsed != 2900*time.Millisecond || up != 20 || down != 20 {
		t.Fatal("boundary lag rejected or distorted measured rate", up, down, elapsed, complete)
	}
	_, _, _, complete = productionTailBandwidth(windows[:2], 3, 3)
	if complete {
		t.Fatal("missing tail window accepted")
	}
	_, _, _, complete = productionTailBandwidth(append(append([]productionBandwidthWindow(nil), windows...), windows[0]), 3, 3)
	if complete {
		t.Fatal("duplicate tail window concealed missing coverage")
	}
	for i := range windows {
		windows[i].Duration = 200 * time.Millisecond
	}
	_, _, _, complete = productionTailBandwidth(windows, 3, 3)
	if complete {
		t.Fatal("short partial interval accepted as complete tail")
	}
}
