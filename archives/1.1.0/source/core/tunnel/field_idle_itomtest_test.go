//go:build itomtest

package tunnel

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"
)

func TestFieldIdleLightReference(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	f := newProductionSupervisorFixtureState(t, 15, fieldSequenceHistory(t))
	p := rev4Open(t, f)
	defer func() { p.Close() }()
	for _, phase := range []string{"idle", "light"} {
		samples := make([]float64, 0, 24)
		failed := 0
		for i := 0; i < 24; i++ {
			if phase == "idle" {
				time.Sleep(150 * time.Millisecond)
			}
			at := time.Now()
			err := rev4Exchange(p, i, 32, 750*time.Millisecond)
			elapsed := float64(time.Since(at)) / float64(time.Millisecond)
			samples = append(samples, elapsed)
			if err != nil {
				failed++
				t.Errorf("%s probe%d: %v", phase, i, err)
				// The timed-out reply can still arrive. A new request on the
				// same byte stream would consume that old reply and misreport
				// every subsequent probe as corruption. Keep the deadline miss
				// as a failure, then use a fresh stream for the next measurement.
				p.Close()
				p = rev4Open(t, f)
			}
		}
		ordered := append([]float64(nil), samples...)
		sort.Float64s(ordered)
		var sum float64
		for _, v := range samples {
			sum += v
		}
		b, _ := json.Marshal(map[string]any{"variant": os.Getenv("ITOM_EXPERIMENT_LABEL"), "phase": phase, "samples_ms": samples, "mean_ms": sum / 24, "p95_ms": ordered[22], "max_ms": ordered[23], "failed": failed, "server_rtt_ms": loadEnvInt(t,"ITOM_LOAD_SERVER_RTT_MS",20,0,1000), "configured_account_sessions": os.Getenv("ITOM_LOAD_ACCOUNT_SESSIONS"), "provider_sensitivity": os.Getenv("ITOM_FIELD_COMMAND_SERVICE"), "field_qualified": false})
		t.Log("FIELD_IDLE " + string(b))
	}
}
