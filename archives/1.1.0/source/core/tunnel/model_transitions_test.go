//go:build itomtest

package tunnel

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A completed download must not leave subsequent short requests paying an old
// bulk window or a congestion hold. Use the same probe TCP before/during/after.
func TestModelBulkToQuiet(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	t.Setenv("ITOM_LOAD_SERVER_RTT_MS", "2")
	t.Setenv("ITOM_LOAD_SERVER_MBPS", "1000")
	t.Setenv("ITOM_LOAD_CLIENT_MBPS", "500")
	f := newProductionSupervisorFixtureAccounts(t, 10)
	chunkBytes := loadEnvInt(t, "ITOM_MODEL_TRANSITION_BYTES", 256<<10, 256<<10, 4<<20)
	probe := rev4Open(t, f)
	defer probe.Close()
	report := func(phase string, count int) {
		samples := make([]float64, 0, count)
		var directStart, directEnd []int64
		for i := 0; i < count; i++ {
			time.Sleep(150 * time.Millisecond)
			at := time.Now()
			directStart = append(directStart, f.network.mobile.profile.Load().RTT.Milliseconds())
			if err := rev4Exchange(probe, i, 32, 3*time.Second); err != nil {
				t.Fatal(phase, i, err)
			}
			samples = append(samples, float64(time.Since(at))/float64(time.Millisecond))
			directEnd = append(directEnd, f.network.mobile.profile.Load().RTT.Milliseconds())
		}
		ordered := append([]float64(nil), samples...)
		sort.Float64s(ordered)
		var mean float64
		for _, v := range samples {
			mean += v
		}
		mean /= float64(count)
		b, _ := json.Marshal(map[string]any{"variant": os.Getenv("ITOM_EXPERIMENT_LABEL"), "phase": phase, "samples_ms": samples, "client_rtt_start_ms": directStart, "client_rtt_end_ms": directEnd, "mean_ms": mean, "p95_ms": ordered[len(ordered)*95/100], "max_ms": ordered[len(ordered)-1], "failed": 0, "field_qualified": false})
		t.Log("MODEL_TRANSITION " + string(b))
	}
	report("before-bulk", 12)
	var stop atomic.Bool
	var bytes atomic.Uint64
	var workers sync.WaitGroup
	start := make(chan struct{})
	var startOnce sync.Once
	startWorkers := func() { startOnce.Do(func() { close(start) }) }
	t.Cleanup(func() { stop.Store(true); startWorkers(); workers.Wait() })
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		c := rev4Open(t, f)
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer c.Close()
			<-start
			for sequence := 0; !stop.Load(); sequence++ {
				if err := rev4Exchange(c, sequence, chunkBytes, 3*time.Second); err != nil {
					errs <- err
					return
				}
				bytes.Add(uint64(chunkBytes))
			}
		}()
	}
	started := time.Now()
	startWorkers()
	report("during-bulk", 12)
	if left := 15*time.Second - time.Since(started); left > 0 {
		timer := time.NewTimer(left)
		select {
		case <-timer.C:
		case err := <-errs:
			timer.Stop()
			t.Fatal("bulk", err)
		}
	}
	stop.Store(true)
	workers.Wait()
	close(errs)
	for err := range errs {
		t.Fatal("bulk completion", err)
	}
	t.Logf("model_transition_bulk useful_mbps_each_direction=%.6f seconds=%.3f bytes=%d chunk_bytes=%d field_qualified=false", float64(bytes.Load())*8/time.Since(started).Seconds()/1e6, time.Since(started).Seconds(), bytes.Load(), chunkBytes)
	// There is no special cleanup/reconnect or controller reset here.
	report("after-bulk", 24)
	if f.targetDials.Load() != 5 || f.carrierExits.unexpected.Load() != 0 {
		t.Fatal("bulk/quiet transition replaced sockets or carriers", f.targetDials.Load())
	}
}
