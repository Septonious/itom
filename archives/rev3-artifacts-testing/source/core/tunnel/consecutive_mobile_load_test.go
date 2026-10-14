package tunnel

import (
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Saturated independent directions, not request/echo pacing. Keep the same
// logical generation and target TCP sockets through repeated 30s loads, a
// capacity drop and idle physical DATA closures. All useful bytes are checked.
func TestConsecutiveConstrainedLoadsAndIdleDATARecovery(t *testing.T) {
	if os.Getenv("ITOM_CONSECUTIVE_MOBILE") != "1" {
		t.Skip("set ITOM_CONSECUTIVE_MOBILE=1 for the sustained shared-link gate")
	}
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", "3")
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", "300")
	t.Setenv("ITOM_LOAD_MOBILE_SHARED_MEDIUM", "1")
	t.Setenv("ITOM_LOAD_MOBILE_LOSS_PERCENT", "0.5")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "20")
	start := time.Now()
	f := newProductionSupervisorFixtureAccounts(t, 10)
	defer func() {
		if t.Failed() {
			fieldLogLoadState(t, f, "constrained-failure")
		}
	}()
	t.Logf("startup_ms=%d shared_link=true synthetic_provider=true", time.Since(start).Milliseconds())
	carriers := snapshotLoadCarriers(t, f, 10, 10)
	target := newProductionLoadTarget(t, "consecutive-mobile")
	logical, generation := f.client.logicalSession, f.client.generation
	var stop atomic.Bool
	var workers sync.WaitGroup
	var sockets []net.Conn
	failures := make(chan error, 32)
	open := func(c wire.Connect) net.Conn {
		p, _, e := f.client.Open(f.ctx, c)
		if e != nil {
			t.Fatal(e)
		}
		n, ok := p.(net.Conn)
		if !ok {
			t.Fatal("connection type")
		}
		sockets = append(sockets, n)
		return n
	}
	for i := 0; i < 8; i++ {
		up, down := open(target.bulkConnect(true, i)), open(target.bulkConnect(false, i))
		workers.Add(2)
		go func(p net.Conn, index int) {
			defer workers.Done()
			<-target.startCh
			pattern := newProductionPattern(target.streamIdentity(true, index))
			buf := make([]byte, 32<<10)
			for !stop.Load() {
				pattern.fill(buf)
				if e := productionWriteFull(p, buf); e != nil {
					if !stop.Load() {
						failures <- e
					}
					return
				}
			}
		}(up, i)
		go func(p net.Conn, index int) {
			defer workers.Done()
			if e := productionReadVerifiedObserver(p, target.streamIdentity(false, index), &stop, &target.downloadDelivered, func(n int) { target.recordWindow(false, n) }); e != nil && !stop.Load() {
				failures <- e
			}
		}(down, i)
	}
	probe := open(target.echoConnect())
	defer func() {
		stop.Store(true)
		target.close()
		for _, p := range sockets {
			p.Close()
		}
		workers.Wait()
	}()
	target.startBulk()
	for round, rate := range []float64{3, 10, 3} {
		f.network.mobile.set(mobileLinkProfile{RTT: 300 * time.Millisecond, Mbps: rate, SharedMedium: true, LossPercent: 0.5, Seed: int64(100 + round)})
		if e := pause(f.ctx, 8*time.Second); e != nil {
			t.Fatal(e)
		}
		result, e := productionMeasure(f.ctx, productionMeasureOptions{Duration: 30 * time.Second, Cadence: 3 * time.Second, ProbeDeadline: 3 * time.Second, MaxGap: 8 * time.Second, BandwidthCadence: time.Second}, []*productionLoadTarget{target}, []net.Conn{probe}, nil, failures, t.Logf)
		if e != nil {
			t.Fatal("saturated load", round, e)
		}
		rtt, _, timeouts, missed := productionSampleSummary(result.Samples, 0)
		up, down, tail, complete := productionTailBandwidth(result.Bandwidth, 30, 10)
		t.Logf("round=%d shared_mbps=%.0f useful_up_mbps=%.3f useful_down_mbps=%.3f tail_up_mbps=%.3f tail_down_mbps=%.3f tail_ms=%d max_echo_ms=%d timeouts=%d missed=%d", round+1, rate, float64(result.Upload[0])*8/result.Elapsed.Seconds()/1e6, float64(result.Download[0])*8/result.Elapsed.Seconds()/1e6, up, down, tail.Milliseconds(), productionPercentile(rtt, 1).Milliseconds(), timeouts, missed)
		if !complete || up < 0.25 || down < 0.25 || timeouts != 0 || missed != 0 || len(rtt) != 10 {
			t.Fatal("sustained stability gate failed")
		}
		assertLoadCarriersPreserved(t, f, carriers)
		if f.client.logicalSession != logical || f.client.generation != generation || f.client.Stats().TCP != 17 {
			t.Fatal("logical TCP identity changed")
		}
	}
	stop.Store(true)
	for _, p := range sockets[:len(sockets)-1] {
		p.Close()
	}
	workers.Wait()
	if e := pause(f.ctx, 4*time.Second); e != nil {
		t.Fatal(e)
	}
	// Provider-style idle closes below the mailbox adapter; no application
	// publication is submitted while the sockets are being independently healed.
	var killed int
	for _, old := range carriers {
		for _, l := range []*lane{old.client, old.server} {
			m := l.mail
			if observed, ok := m.(*observedMail); ok {
				m = observed.Transport
			}
			lm, ok := m.(*laneMail)
			if !ok {
				t.Fatal("physical mailbox scope")
			}
			var clients []*mailbox.Client
			if lm.runtime != nil {
				lm.runtime.mu.Lock()
				for _, c := range lm.runtime.workerSockets {
					if c, ok := c.(*mailbox.Client); ok {
						clients = append(clients, c)
					}
				}
				lm.runtime.mu.Unlock()
			} else {
				lm.writerMu.Lock()
				clients = append(clients, lm.writers...)
				lm.writerMu.Unlock()
			}
			for _, c := range clients {
				if c == nil {
					continue
				}
				if f.network.dropTuple(c.LocalAddr().String(), c.RemoteAddr().String()) {
					killed++
				}
			}
		}
	}
	if killed < 10 {
		t.Fatal("idle close injection did not cover pool", killed)
	}
	if e := pause(f.ctx, 5*time.Second); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		if e := rev3Echo(probe, []byte(fmt.Sprintf("same post-load TCP/%d", i)), 3*time.Second); e != nil {
			t.Fatal("post-load connection", e)
		}
	}
	assertLoadCarriersPreserved(t, f, carriers)
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("post-load session replaced")
	}
	t.Logf("post_load_idle_sockets_closed=%d same_echo_socket=true integrity_errors=0", killed)
}

func (p *providerTestNetwork) dropTuple(peer, local string) bool {
	p.mu.Lock()
	var ids []uint64
	for id, c := range p.active {
		if c.originalPeer == peer && c.originalLocal == local {
			ids = append(ids, id)
		}
	}
	p.mu.Unlock()
	return p.closeIDs(ids) > 0
}
