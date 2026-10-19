package tunnel

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// Unlike a speedtest this qualifies a degraded network for survival and
// bounded restoration. A test cannot pass merely by stopping the traffic or
// creating a replacement application socket after a carrier loss.
func TestUnstableSequentialAccountOutagesKeepApplicationSockets(t *testing.T) {
	if os.Getenv("ITOM_UNSTABLE_SURVIVAL") != "1" {
		t.Skip("enable ITOM_UNSTABLE_SURVIVAL=1 for fault-injection qualification")
	}
	f := newProductionSupervisorFixtureAccounts(t, 3)
	f.network.setMbps(12)
	first := f.openOn(t, "a01")
	second := f.openOn(t, "a02")
	session, generation := f.client.logicalSession, f.client.generation
	checkedEcho := func(label string, p *pipe) {
		_ = p.SetDeadline(time.Now().Add(10 * time.Second))
		t.Helper()
		data := bytes.Repeat([]byte(label), 512) // independent verified TCP payload
		if err := productionWriteFull(p, data); err != nil {
			t.Fatal("application stopped writing", label, err)
		}
		read := make([]byte, len(data))
		if _, err := io.ReadFull(p, read); err != nil {
			t.Fatal("application stopped reading", label, err)
		}
		if !bytes.Equal(data, read) {
			t.Fatal("application bytes corrupted", label)
		}
	}
	checkedEcho("initial/one", first)
	checkedEcho("initial/two", second)
	for round := 0; round < 3; round++ {
		id := fmt.Sprintf("a%02d", round%2+1)
		old := f.waitLane(t, id, 0)
		dropped := f.outageAccount(id, 1500*time.Millisecond)
		t.Logf("unstable_fault round=%d account=%s dropped=%d", round+1, id, dropped)
		// Always keep sending through a different, already-open application socket.
		if id == "a01" {
			checkedEcho(fmt.Sprintf("surviving/%d", round), second)
		} else {
			checkedEcho(fmt.Sprintf("surviving/%d", round), first)
		}
		rejoined := f.waitLane(t, id, old.carrierEpoch)
		if rejoined.logicalSession != session || rejoined.generation != generation {
			t.Fatal("reconnect replaced logical session")
		}
		checkedEcho(fmt.Sprintf("restored/one/%d", round), first)
		checkedEcho(fmt.Sprintf("restored/two/%d", round), second)
		if f.client.Stats().TCP != 2 || f.targetDials.Load() != 2 {
			t.Fatalf("application socket was replaced: client=%d target=%d", f.client.Stats().TCP, f.targetDials.Load())
		}
	}
	t.Logf("unstable_survival verified_tcp_streams=2 outage_rounds=3 session_preserved=true bounded_rejoin=true field_qualified=false")
}

// Low throughput and repeated physical outages must not cause a global stall.
// Keep every one of the eight TCP sockets open and verify distinct payloads on
// each, rather than assessing Mbps during deliberately restricted intervals.
func TestUnstableEightTCPStreamsProgressAfterOutages(t *testing.T) {
	if os.Getenv("ITOM_UNSTABLE_SURVIVAL") != "1" {
		t.Skip("set ITOM_UNSTABLE_SURVIVAL=1")
	}
	f := newProductionSupervisorFixtureAccounts(t, 4)
	sockets := make([]*pipe, 8)
	for i := range sockets {
		sockets[i] = f.openOn(t, fmt.Sprintf("a%02d", i%4+1))
	}
	logical, generation := f.client.logicalSession, f.client.generation
	for round, mbps := range []float64{5, 12, 25} {
		f.network.setMbps(mbps)
		id := fmt.Sprintf("a%02d", round+1)
		old := f.waitLane(t, id, 0)
		f.outageAccount(id, 1200*time.Millisecond)
		// Send on all sockets, including owners of the offline account. Stripes
		// must continue to make progress on surviving physical connections.
		var wg sync.WaitGroup
		failures := make(chan error, len(sockets))
		for i, p := range sockets {
			wg.Add(1)
			go func(i int, p *pipe) {
				defer wg.Done()
				_ = p.SetDeadline(time.Now().Add(14 * time.Second))
				payload := bytes.Repeat([]byte(fmt.Sprintf("round=%d/stream=%d/", round, i)), 1024)
				if err := productionWriteFull(p, payload); err != nil {
					failures <- fmt.Errorf("write stream %d: %w", i, err)
					return
				}
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(p, got); err != nil {
					failures <- fmt.Errorf("read stream %d: %w", i, err)
					return
				}
				if !bytes.Equal(payload, got) {
					failures <- fmt.Errorf("stream %d corrupt", i)
				}
			}(i, p)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Fatal("unstable TCP session failed while provider degraded", err)
		}
		rejoined := f.waitLane(t, id, old.carrierEpoch)
		if rejoined.logicalSession != logical || rejoined.generation != generation {
			t.Fatal("lost authenticated session identity after outage")
		}
		if f.client.Stats().TCP != len(sockets) || f.targetDials.Load() != int64(len(sockets)) {
			t.Fatalf("TCP socket lost or reopened: local=%d remote=%d", f.client.Stats().TCP, f.targetDials.Load())
		}
		t.Logf("unstable_multistream round=%d provider_mbps=%.0f verified_streams=%d recovered_account=%s", round+1, mbps, len(sockets), id)
	}
}

// Even a complete but temporary provider blackout must not require restarting
// ITOM or opening a new application TCP socket. The write is deliberately
// initiated while every carrier is offline and has a strict recovery deadline.
func TestUnstableWholeProviderBlackoutRestoresPendingTCP(t *testing.T) {
	if os.Getenv("ITOM_UNSTABLE_SURVIVAL") != "1" {
		t.Skip("set ITOM_UNSTABLE_SURVIVAL=1")
	}
	f := newProductionSupervisorFixtureAccounts(t, 3)
	p := f.openOn(t, "a01")
	if err := supervisorEcho(p, 0); err != nil {
		t.Fatal("pre-blackout TCP", err)
	}
	var epochs [3]uint64
	for i := range epochs {
		epochs[i] = f.waitLane(t, fmt.Sprintf("a%02d", i+1), 0).carrierEpoch
	}
	logical, generation := f.client.logicalSession, f.client.generation
	for i := range epochs {
		f.outageAccount(fmt.Sprintf("a%02d", i+1), 2500*time.Millisecond)
	}
	_ = p.SetDeadline(time.Now().Add(40 * time.Second))
	payload := bytes.Repeat([]byte("pending-while-all-imap-sockets-offline-"), 256)
	if err := productionWriteFull(p, payload); err != nil {
		t.Fatal("blackout pending TCP write never recovered", err)
	}
	received := make([]byte, len(payload))
	if _, err := io.ReadFull(p, received); err != nil {
		t.Fatal("blackout pending TCP reply never recovered", err)
	}
	if !bytes.Equal(payload, received) {
		t.Fatal("pending DATA corrupted across blackout")
	}
	for i := range epochs {
		l := f.waitLane(t, fmt.Sprintf("a%02d", i+1), epochs[i])
		if l.logicalSession != logical || l.generation != generation {
			t.Fatal("provider recovery silently replaced session")
		}
	}
	if f.client.Stats().TCP != 1 || f.targetDials.Load() != 1 {
		t.Fatal("blackout recovery silently replaced TCP socket")
	}
	t.Log("unstable_blackout recovered=true verified_tcp=true new_tcp_dials=0 application_restart=false")
}
