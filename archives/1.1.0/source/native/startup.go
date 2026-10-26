package mobile

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"itom.local/core/tunnel"
)

// This heartbeat exists only while New owns startup. It reports actual SELECT
// progress without declaring usable VPN lines. Joining it before New returns
// prevents callbacks into a stopped/replaced Android service generation.
func reportStartup(ctx context.Context, cancel context.CancelFunc, host Host, total int) (func(tunnel.StartupProgress), func()) {
	var mu sync.Mutex
	state := tunnel.StartupProgress{Stage: "PROFILE", TotalLines: total}
	started := time.Now()
	send := func() bool {
		mu.Lock()
		copy := state
		mu.Unlock()
		raw, _ := json.Marshal(struct {
			Revision, StartupStage, ConfigHash string
			SelectedLines, TotalLines          int
			ElapsedMS, SampledAtMS             int64
		}{tunnel.TransportRevision, copy.Stage, copy.ConfigHash, copy.SelectedLines, copy.TotalLines, time.Since(started).Milliseconds(), time.Now().UnixMilli()})
		return host.Startup(string(raw))
	}
	if !send() {
		cancel()
	}
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-run.Done():
				return
			case <-ticker.C:
				if !send() {
					cancel()
					return
				}
			}
		}
	}()
	return func(p tunnel.StartupProgress) {
		mu.Lock()
		state = p
		mu.Unlock()
	}, func() { stop(); <-done }
}
