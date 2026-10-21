package tunnel

import (
	"os"
	"testing"
	"time"
)

func startModelMobileTrace(t *testing.T, p *providerTestNetwork) {
	if os.Getenv("ITOM_MODEL_MOBILE_TRACE") != "1" {
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(stop); <-done })
	go func() {
		defer close(done)
		trace := []time.Duration{40, 80, 180, 60, 140, 45, 120, 180, 70, 40}
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			next := *p.mobile.profile.Load()
			next.RTT = trace[i%len(trace)] * time.Millisecond
			p.mobile.profile.Store(&next)
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	t.Log("model_mobile_trace_ms=40,80,180,60,140,45,120,180,70,40 interval_ms=1500 clock_preserved=true server_path_unchanged=true")
}
