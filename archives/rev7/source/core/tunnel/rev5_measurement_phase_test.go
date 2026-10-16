package tunnel

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// The first zero-phase probe used only its own immediate echo to observe bulk
// progress; later probes used a full preceding cadence. A centered schedule
// gives the initial period an observation span without dropping any probe or
// shifting/shortening the independent bandwidth measurement. One-sided load
// still cannot become a full-duplex overlap.
func TestRev5ProbePhasePreservesCountClockAndRejectsUnloadedDirection(t *testing.T) {
	for _, duplex := range []bool{true, false} {
		t.Run(map[bool]string{true: "duplex", false: "no_download"}[duplex], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a, b := net.Pipe()
			defer a.Close()
			defer b.Close()
			go io.Copy(b, b)
			target := &productionLoadTarget{errors: make(chan error, 1)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				tick := time.NewTicker(10 * time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
						target.uploadDelivered.Add(1024)
						if duplex {
							target.downloadDelivered.Add(1024)
						}
					}
				}
			}()
			result, err := productionMeasure(ctx, productionMeasureOptions{Duration: 600 * time.Millisecond, Cadence: 200 * time.Millisecond, ProbeOffset: 100 * time.Millisecond,
				BandwidthCadence: 100 * time.Millisecond, ProbeDeadline: 100 * time.Millisecond}, []*productionLoadTarget{target}, []net.Conn{a}, nil, nil, t.Logf)
			cancel()
			<-done
			if err != nil || len(result.Samples) != 3 || len(result.Bandwidth) != 6 || result.Elapsed != 600*time.Millisecond {
				t.Fatal("probe count or complete byte clock changed", result, err)
			}
			for i, s := range result.Samples {
				if s.Index != i+1 || s.Scheduled != time.Duration(100+200*i)*time.Millisecond || s.Status != "ok" || s.RTT >= 100*time.Millisecond || s.Overlap != duplex {
					t.Fatal("cadence, measured RTT or loaded-direction evidence changed", s)
				}
			}
		})
	}
}
