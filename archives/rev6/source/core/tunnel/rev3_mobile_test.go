package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMobilePressureQuicklyBoundsNewFlightWithoutRefund(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	r := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now.Add(-2 * time.Second)}
	d.ranges[r.key] = r
	d.bulk = r.end
	for i := 0; i < 6; i++ {
		d.maintain(now.Add(time.Duration(i)*deliveryDecisionEpoch), true)
	}
	if d.window != 64<<10 {
		t.Fatal("congestion was not bounded promptly", d.window)
	}
	if d.bulk != r.end || len(d.ranges) != 1 || d.delivered != 0 {
		t.Fatal("congestion refunded unacknowledged DATA")
	}
	if logicalProgressLimit <= 32*time.Second || logicalProgressLimit >= 120*time.Second {
		t.Fatal("logical outage retention is unbounded or too short")
	}
}

func TestParallelClientDialDoesNotEraseSiblingFailureCooldown(t *testing.T) {
	g := newAccountDialGate()
	g.slot = make(chan struct{}, 2)
	begun := make(chan struct{})
	finish := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- g.run(context.Background(), func() error { close(begun); <-finish; return nil }) }()
	<-begun
	if err := g.run(context.Background(), func() error { return errors.New("IMAP_CONNECT_FAILED") }); err == nil {
		t.Fatal("failure lost")
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	next := g.next
	g.mu.Unlock()
	if time.Until(next) <= 0 {
		t.Fatal("earlier successful sibling erased newer failure cooldown")
	}
}

type noInventoryMail struct {
	mailbox.Transport
	calls int
}

func (m *noInventoryMail) FetchAll(context.Context, string) ([]mailbox.Message, error) {
	m.calls++
	return nil, errors.New("UNNECESSARY_INVENTORY")
}
func TestEmptyLocalJournalSkipsNetworkInventory(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := &noInventoryMail{}
	if _, err = recoverLocal(context.Background(), m, s, u.Up); err != nil || m.calls != 0 {
		t.Fatal("empty journal scanned provider", err, m.calls)
	}
}

func rev3Echo(c net.Conn, body []byte, timeout time.Duration) error {
	if err := c.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	got := make([]byte, len(body))
	if err := productionWriteFull(c, body); err != nil {
		return err
	}
	if _, err := io.ReadFull(c, got); err != nil {
		return err
	}
	if !bytes.Equal(body, got) {
		return fmt.Errorf("TCP payload mismatch")
	}
	return nil
}

// Production supervisors and TLS IMAP, shared mobile byte clock with ordered
// loss/retransmissions. This is a synthetic reliable-stream model, not netem.
func TestNarrowMobileTenAccountsCommonOutageAndIndependentTracing(t *testing.T) {
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", "300")
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", "3")
	t.Setenv("ITOM_LOAD_MOBILE_SHARED_MEDIUM", "1")
	t.Setenv("ITOM_LOAD_MOBILE_LOSS_PERCENT", "0.5")
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	t.Setenv("ITOM_LOAD_SERVER_DIAGNOSTICS_OFF", "1")
	started := time.Now()
	f := newProductionSupervisorFixtureAccounts(t, 10)
	ready := time.Since(started)
	t.Logf("startup_20_of_20_ms=%d", ready.Milliseconds())
	if f.client.Stats().ActiveLines != 20 || ready > 20*time.Second {
		t.Fatal("startup/readiness", ready, f.client.Stats())
	}
	sockets := make([]net.Conn, 4)
	for i := range sockets {
		sockets[i] = f.openOn(t, fmt.Sprintf("a%02d", i+1))
	}
	body := bytes.Repeat([]byte("mobile-payload-integrity/"), 1366)
	for phase, rate := range []float64{3, 10, 3} {
		if !f.network.mobile.set(mobileLinkProfile{RTT: 300 * time.Millisecond, Mbps: rate, SharedMedium: true, LossPercent: 0.5, Seed: int64(phase + 1)}) {
			t.Fatal("profile")
		}
		phaseStart := time.Now()
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		counts := make(chan int64, 4)
		for _, c := range sockets {
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				var n int64
				for time.Since(phaseStart) < 6*time.Second {
					if err := rev3Echo(c, body, 20*time.Second); err != nil {
						errs <- err
						return
					}
					n += int64(len(body))
				}
				counts <- n
			}(c)
		}
		wg.Wait()
		close(errs)
		close(counts)
		for err := range errs {
			t.Fatal("mobile payload", phase, err)
		}
		var n int64
		for count := range counts {
			n += count
		}
		mbps := float64(n) * 8 / time.Since(phaseStart).Seconds() / 1e6
		t.Logf("mobile_phase=%d shared_mbps=%.0f useful_duplex_mbps=%.3f", phase, rate, mbps)
		if mbps < 0.25 {
			t.Fatal("insufficient useful payload", phase, mbps)
		}
	}
	targets := f.targetDials.Load()
	logical, generation := f.client.logicalSession, f.client.generation
	for i := 1; i <= 10; i++ {
		f.outageAccount(fmt.Sprintf("a%02d", i), 32*time.Second)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, c := range sockets {
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			errs <- rev3Echo(c, []byte("same TCP after common outage"), 80*time.Second)
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal("common outage closed existing socket", err)
		}
	}
	for i := 1; i <= 10; i++ {
		f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
	}
	if f.targetDials.Load() != targets || f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("outage reopened TCP/session")
	}
	if f.serverLogs.Load() != 0 {
		t.Fatal("disabled server emitted logs", f.serverLogs.Load())
	}
	files, _ := filepath.Glob(filepath.Join(f.serverDir, "transport-trace*"))
	if len(files) != 0 {
		t.Fatal("disabled server wrote traces", files)
	}
	if f.client.tracer == nil || f.client.tracer.status().Written == 0 {
		t.Fatal("server switch disabled client tracing")
	}
	raw, err := os.ReadFile(filepath.Join(f.clientDir, "transport-trace.jsonl"))
	if err != nil || len(raw) == 0 {
		t.Fatal("client trace missing", err)
	}
	t.Log("common_outage_seconds=32 same_four_TCP_connections=true server_logs=0 server_trace_files=0 client_trace_active=true")
}
