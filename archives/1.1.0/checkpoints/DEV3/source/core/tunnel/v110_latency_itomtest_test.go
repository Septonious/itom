//go:build itomtest

package tunnel

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// Real supervisors, TLS/IMAP, actual journals/fsync and persistent application
// sockets. Routes are independent; no artificial outage or slow journal replay.
func TestV110IdleLightReference(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	serverRTT := loadEnvInt(t, "ITOM_LOAD_SERVER_RTT_MS", 20, 0, 1000)
	t.Setenv("ITOM_LOAD_SERVER_RTT_MS", strconv.Itoa(serverRTT))
	t.Setenv("ITOM_LOAD_CLIENT_MBPS", "500")
	t.Setenv("ITOM_LOAD_SERVER_MBPS", "1000")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	f := newProductionSupervisorFixtureAccounts(t, 10)
	c := rev4Open(t, f)
	defer c.Close()
	for i := 0; i < 4; i++ {
		if err := rev4Exchange(c, i, 32, 3*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	var stop atomic.Bool
	done := make(chan error, 1)
	report := func(phase string) {
		samples := make([]float64, 0, 24)
		for i := 0; i < 24; i++ {
			// A sparse request waits longer; normal cadence includes light interactive bursts.
			pause := time.Duration(loadEnvInt(t, "ITOM_V110_IDLE_PAUSE_MS", 150, 1, 10000)) * time.Millisecond
			if i%8 == 0 {
				pause = max(pause, 800*time.Millisecond)
			}
			time.Sleep(pause)
			started := time.Now()
			if err := rev4Exchange(c, i, 32, 3*time.Second); err != nil {
				t.Fatal(phase, err)
			}
			samples = append(samples, float64(time.Since(started))/float64(time.Millisecond))
		}
		sorted := append([]float64{}, samples...)
		sort.Float64s(sorted)
		var mean float64
		for _, v := range samples {
			mean += v
		}
		mean /= float64(len(samples))
		b, _ := json.Marshal(map[string]any{"variant": os.Getenv("ITOM_EXPERIMENT_LABEL"), "phase": phase, "client_rtt_ms": os.Getenv("ITOM_LOAD_CLIENT_RTT_MS"), "server_rtt_ms": serverRTT, "samples_ms": samples, "mean_ms": mean, "p50_ms": sorted[len(sorted)/2], "p95_ms": sorted[len(sorted)*95/100], "max_ms": sorted[len(sorted)-1], "failures": 0, "accounts": 10, "request_pause_ms": loadEnvInt(t, "ITOM_V110_IDLE_PAUSE_MS", 150, 1, 10000), "field_qualified": false})
		t.Log("V110_RESULT " + string(b))
	}
	report("idle")
	b := rev4Open(t, f)
	defer b.Close()
	go func() {
		i := 0
		for !stop.Load() {
			if err := rev4Exchange(b, i, 4096, 3*time.Second); err != nil {
				done <- err
				return
			}
			i++
			time.Sleep(100 * time.Millisecond)
		}
		done <- nil
	}()
	report("light")
	stop.Store(true)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f.targetDials.Load() != 2 {
		t.Fatal("application sockets replaced", f.targetDials.Load())
	}
}
