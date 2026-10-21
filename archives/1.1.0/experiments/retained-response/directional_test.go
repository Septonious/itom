//go:build itomtest

package tunnel

import (
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// This additional workload measures one active bulk direction. It does not
// replace or waive any duplex qualification assertion or count toward its20 rounds.
func TestRetainedDirectionalCapacity(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	var retained func(string, string, servercfg.Config, servercfg.User)
	if os.Getenv("ITOM_LOAD_RETAINED_STATE") == "1" {
		retained = rev4RetainedState(t)
	}
	f := newProductionSupervisorFixtureState(t, 10, retained)
	logical, generation := f.client.logicalSession, f.client.generation
	target := newProductionLoadTarget(t, fmt.Sprintf("%x/directional", logical))
	upload := os.Getenv("ITOM_DIRECTION") == "upload"
	if !upload && os.Getenv("ITOM_DIRECTION") != "download" {
		t.Fatal("direction must be upload or download")
	}
	open := func(dst wire.Connect) net.Conn {
		c, _, err := f.client.Open(f.ctx, dst)
		if err != nil {
			t.Fatal(err)
		}
		p, ok := c.(net.Conn)
		if !ok {
			c.Close()
			t.Fatal("application stream has no deadline")
		}
		return p
	}
	bulk := open(target.bulkConnect(upload, 0))
	probe := open(target.echoConnect())
	var stop atomic.Bool
	var workers sync.WaitGroup
	failures := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		if upload {
			<-target.startCh
			pattern := newProductionPattern(target.streamIdentity(true, 0))
			buf := make([]byte, target.chunkBytes)
			for !stop.Load() {
				pattern.fill(buf)
				if err := productionWriteFull(bulk, buf); err != nil {
					if !stop.Load() {
						failures <- err
					}
					return
				}
			}
		} else if err := productionReadVerifiedObserver(bulk, target.streamIdentity(false, 0), &stop, &target.downloadDelivered, func(n int) { target.recordWindow(false, n) }); err != nil && !stop.Load() {
			failures <- err
		}
	}()
	defer func() { stop.Store(true); target.close(); bulk.Close(); probe.Close(); workers.Wait() }()
	target.startBulk()
	if err := pause(f.ctx, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	carriers := snapshotLoadCarriers(t, f, 10, 10)
	result, err := productionMeasure(f.ctx, productionMeasureOptions{Duration: 60 * time.Second, Cadence: 2 * time.Second, ProbeOffset: time.Second, BandwidthCadence: time.Second, ProbeDeadline: 1500 * time.Millisecond}, []*productionLoadTarget{target}, []net.Conn{probe}, f.carrierExits.failures, failures, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	bytes := result.Download[0]
	if upload {
		bytes = result.Upload[0]
	}
	rate := float64(bytes*8) / result.Elapsed.Seconds() / 1e6
	minimum := rate
	for _, span := range []int{30, 10} {
		up, down, duration, complete := productionTailBandwidth(result.Bandwidth, 60, span)
		active := down
		if upload {
			active = up
		}
		minimum = min(minimum, active)
		if !complete || duration != time.Duration(span)*time.Second {
			t.Fatal("incomplete directional tail", span, duration)
		}
		t.Logf("directional_tail direction=%s duration_ms=%d useful_mbps=%.6f", os.Getenv("ITOM_DIRECTION"), duration.Milliseconds(), active)
	}
	for _, w := range result.Bandwidth {
		active := w.Download
		if upload {
			active = w.Upload
		}
		if active <= 0 {
			t.Errorf("active direction stalled in one-second window %d", w.Index)
		}
	}
	rtts, _, timeouts, missed := productionSampleSummary(result.Samples, 0)
	if len(rtts) != 30 || timeouts != 0 || missed != 0 {
		t.Error("directional probes incomplete", len(rtts), timeouts, missed)
	}
	if minimum < 100 {
		t.Error("directional useful full/tail rate below unchanged100Mbps target", minimum)
	}
	assertLoadCarriersPreserved(t, f, carriers)
	target.mu.Lock()
	targetConnections := len(target.connections)
	target.mu.Unlock()
	if targetConnections != 2 || f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("directional workload replaced socket/session", targetConnections)
	}
	if err := target.firstError(); err != nil {
		t.Fatal(err)
	}
	t.Logf("directional_result variant=%s direction=%s retained=%s server_rtt_ms=%d useful_mbps=%.6f minimum_full_tail_mbps=%.6f p95_ms=%.3f max_ms=%.3f probes=%d timeouts=%d missed=%d socket_identity=true field_qualified=false original_duplex_gates_unchanged=true", os.Getenv("ITOM_EXPERIMENT_LABEL"), os.Getenv("ITOM_DIRECTION"), os.Getenv("ITOM_LOAD_RETAINED_STATE"), loadEnvInt(t, "ITOM_LOAD_SERVER_RTT_MS", 20, 0, 1000), rate, minimum, float64(productionPercentile(rtts, .95))/float64(time.Millisecond), float64(productionPercentile(rtts, 1))/float64(time.Millisecond), len(rtts), timeouts, missed)
}
