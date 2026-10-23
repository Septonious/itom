package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// The Rev3 field capture contains retained server allocations, short exchanges
// mixed with large transfers, correlated receive failure and a second transfer
// after rejoin. Fresh unlimited echo throughput does not exercise that lifecycle.
// These tests use the public supervisors, real TLS/IMAP sockets and fsync.
func rev4RetainedState(t *testing.T) func(string, string, servercfg.Config, servercfg.User) {
	return func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
		for _, a := range cfg.Accounts {
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			// No client wallet history: reinstall never manufactures a release
			// proof for these allocations. Recovery must use remaining capacity.
			if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 6 << 20, Messages: 130}); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func rev4Exchange(c net.Conn, label int, size int, deadline time.Duration) error {
	_ = c.SetDeadline(time.Now().Add(deadline))
	want := make([]byte, size)
	for i := range want {
		want[i] = byte((i*131 + label*17) % 251)
	}
	written := make(chan error, 1)
	go func() { written <- productionWriteFull(c, want) }()
	got := make([]byte, len(want))
	read, readErr := io.ReadFull(c, got)
	if readErr != nil {
		return fmt.Errorf("read %d/%d bytes: %w", read, size, readErr)
	}
	if err := <-written; err != nil {
		return err
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("stream payload corrupted: %d", label)
	}
	return nil
}

func rev4Open(t *testing.T, f *productionSupervisorFixture) net.Conn {
	t.Helper()
	_, port, err := net.SplitHostPort(f.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var n int
	fmt.Sscanf(port, "%d", &n)
	ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
	defer cancel()
	p, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(n)})
	if err != nil {
		f.client.mu.Lock()
		for id, l := range f.client.lanes {
			t.Logf("failed_open_carrier account=%s ready=%t rejoin=%t progress_age_ms=%d debt=%d urgent=%d ordinary=%d", id, l.ready.Load(), l.rejoinPending.Load(), l.remoteProgressAge(time.Now()).Milliseconds(), l.controlDebt.Load(), len(l.urgentQueue), len(l.controlQueue))
		}
		f.client.mu.Unlock()
		t.Logf("failed_open_target_dials=%d", f.targetDials.Load())
		t.Fatal("public application OPEN", err)
	}
	t.Cleanup(func() { p.Close() })
	conn, ok := p.(net.Conn)
	if !ok {
		t.Fatal("public stream lacks test deadlines")
	}
	return conn
}

func rev4RecoveryFailure(t *testing.T, f *productionSupervisorFixture) {
	t.Helper()
	if dir := os.Getenv("ITOM_REV4_DEBUG_FAILURE_DIR"); dir != "" {
		if os.MkdirAll(dir, 0700) == nil {
			file, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%d.txt", t.Name(), time.Now().UnixNano())))
			if err == nil {
				pprof.Lookup("goroutine").WriteTo(file, 2)
				file.Close()
			}
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, carrier := range f.serverLanes {
		g := carrier.stripe
		g.mu.Lock()
		t.Logf("recovery_failure account=%s repair_busy=%t flight=%d window=%d waiting=%d", id, g.delivery.repair, g.delivery.bulk, g.delivery.window, len(g.delivery.waiters))
		for _, r := range g.delivery.ranges {
			t.Logf("recovery_failure_range stream=%d offset=%d end=%d repaired=%t repair_age=%s accepted_age=%s", r.key.stream, r.start, r.end, r.repaired, time.Since(r.repairAccepted), time.Since(r.accepted))
		}
		g.mu.Unlock()
		break
	}
}

func rev4FieldFixture(t *testing.T, retained bool) *productionSupervisorFixture {
	t.Helper()
	if base := os.Getenv("ITOM_LOAD_TRACE_DIR"); base != "" {
		t.Setenv("ITOM_LOAD_TRACE_DIR", filepath.Join(base, fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())))
	}
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "50")
	t.Setenv("ITOM_LOAD_DATA_SHARDS", fmt.Sprint(loadEnvInt(t, "ITOM_REV4_TEST_SHARDS", 4, 1, 4)))
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	var setup func(string, string, servercfg.Config, servercfg.User)
	if retained {
		setup = rev4RetainedState(t)
		// Keep real provider allocation history as well as retained quota. The
		// previous fixture left UIDNEXT=1 and missed the field DATA-reader stall.
		t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	}
	f := newProductionSupervisorFixtureState(t, 4, setup)
	f.network.setMbps(40)
	return f
}

func TestRev4RetainedStateMixedTrafficAndNextTransfer(t *testing.T) {
	f := rev4FieldFixture(t, true)
	chat := f.openOn(t, "a01")
	if err := rev4Exchange(chat, 1, 4096, 3*time.Second); err != nil {
		t.Fatal("initial application exchange", err)
	}
	logical, generation := f.client.logicalSession, f.client.generation
	for round := 0; round < 2; round++ {
		var wg sync.WaitGroup
		failures := make(chan error, 12)
		for i := 0; i < 12; i++ {
			p := f.openOn(t, fmt.Sprintf("a%02d", i%4+1))
			wg.Add(1)
			go func(i int, p *pipe) {
				defer wg.Done()
				defer p.Close()
				if err := rev4Exchange(p, 10+round*12+i, 512<<10, 20*time.Second); err != nil {
					failures <- fmt.Errorf("round %d stream %d: %w", round, i, err)
				}
			}(i, p)
		}
		for i := 0; i < 6; i++ {
			if err := rev4Exchange(chat, 100+round*6+i, 4096, 3*time.Second); err != nil {
				t.Fatal("application unavailable during transfer", err)
			}
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if t.Failed() {
			return
		}
		if err := rev4Exchange(chat, 200+round, 128<<10, 5*time.Second); err != nil {
			t.Fatal("application unavailable after load", err)
		}
	}
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("session replaced")
	}
	f.mu.Lock()
	lanes := make([]*lane, 0, len(f.serverLanes))
	for _, l := range f.serverLanes {
		lanes = append(lanes, l)
	}
	f.mu.Unlock()
	for _, l := range lanes {
		if err := l.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRev4InterruptedBulkCannotStrandApplicationAfterRejoin(t *testing.T) {
	f := rev4FieldFixture(t, true)
	chat := f.openOn(t, "a01")
	if err := rev4Exchange(chat, 1, 4096, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var old [4]*lane
	for i := range old {
		old[i] = f.waitLane(t, fmt.Sprintf("a%02d", i+1), 0)
	}
	sockets := make([]*pipe, 12)
	for i := range sockets {
		sockets[i] = f.openOn(t, fmt.Sprintf("a%02d", i%4+1))
	}
	var wg sync.WaitGroup
	for i, p := range sockets {
		wg.Add(1)
		go func(i int, p *pipe) { defer wg.Done(); _ = rev4Exchange(p, 10+i, 2<<20, 30*time.Second) }(i, p)
	}
	if err := pause(f.ctx, time.Second); err != nil {
		t.Fatal(err)
	}
	for i := range old {
		f.outageAccount(fmt.Sprintf("a%02d", i+1), 2500*time.Millisecond)
	}
	// The speed-test application cancels; chat must remain on its original TCP.
	for _, p := range sockets {
		p.Close()
	}
	wg.Wait()
	for i, l := range old {
		f.waitLane(t, fmt.Sprintf("a%02d", i+1), l.carrierEpoch)
	}
	if err := rev4Exchange(chat, 99, 128<<10, 8*time.Second); err != nil {
		t.Fatal("reconnected lanes left application dead", err)
	}
	next := rev4Open(t, f)
	if err := rev4Exchange(next, 100, 1<<20, 12*time.Second); err != nil {
		t.Fatal("next transfer cannot initialize/progress", err)
	}
}

func TestRev4ParallelApplicationStartupOnMobilePath(t *testing.T) {
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", "120")
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", "40")
	f := rev4FieldFixture(t, true)
	_, port, _ := net.SplitHostPort(f.listener.Addr().String())
	var targetPort int
	fmt.Sscanf(port, "%d", &targetPort)
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
			defer cancel()
			stream, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(targetPort)})
			if err != nil {
				failures <- fmt.Errorf("parallel initialization %d: %w", i, err)
				return
			}
			defer stream.Close()
			conn, ok := stream.(net.Conn)
			if !ok {
				failures <- fmt.Errorf("missing application deadlines")
				return
			}
			if err := rev4Exchange(conn, i, 32<<10, 8*time.Second); err != nil {
				failures <- fmt.Errorf("parallel exchange %d: %w", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 100, 128<<10, 8*time.Second); err != nil {
		t.Fatal("application stalled after startup burst", err)
	}
}

func TestRev4LongBlackoutPreservesInflightTCPAndMessages(t *testing.T) {
	f := rev4FieldFixture(t, true)
	chat := rev4Open(t, f)
	if err := rev4Exchange(chat, 1, 4096, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	var old [4]*lane
	for i := range old {
		old[i] = f.waitLane(t, fmt.Sprintf("a%02d", i+1), 0)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		p := f.openOn(t, fmt.Sprintf("a%02d", i%4+1))
		wg.Add(1)
		go func(i int, p *pipe) {
			defer wg.Done()
			defer p.Close()
			if err := rev4Exchange(p, 10+i, 2<<20, 60*time.Second); err != nil {
				failures <- err
			}
		}(i, p)
	}
	if err := pause(f.ctx, 500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	for i := range old {
		f.outageAccount(fmt.Sprintf("a%02d", i+1), 30*time.Second)
	}
	// A pending application request must survive the whole physical outage.
	if err := rev4Exchange(chat, 100, 128<<10, 55*time.Second); err != nil {
		rev4RecoveryFailure(t, f)
		t.Fatal("original application socket did not recover", err)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		rev4RecoveryFailure(t, f)
		t.Error("inflight TCP lost across blackout", err)
	}
	for i, l := range old {
		f.waitLane(t, fmt.Sprintf("a%02d", i+1), l.carrierEpoch)
	}
	next := rev4Open(t, f)
	if err := rev4Exchange(next, 200, 256<<10, 10*time.Second); err != nil {
		t.Fatal("new application request dead after reconnect", err)
	}
}
