package tunnel

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/mailbox"
	"net"
	"os"
	"regexp"
	"sync"
	"testing"
	"time"
)

func TestMobileRelayRetransmissionPreservesBytesAndCloses(t *testing.T) {
	network := newProviderTestNetwork(0)
	network.mobileEnabled = true
	if !network.mobile.set(mobileLinkProfile{RTT: 2 * time.Millisecond, LossPercent: 5, Mbps: 1000, Seed: 41}) {
		t.Fatal("profile")
	}
	peer, wire := net.Pipe()
	server := network.wrap("a01", wire).(*providerTestConn)
	server.mobileClient.Store(true)
	relay := server.Conn.(*mobileRelay)
	want := bytes.Repeat([]byte("independent TLS wire bytes"), 20000)
	done := make(chan error, 1)
	go func() { _, err := peer.Write(want); done <- err }()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(server, got); err != nil || !bytes.Equal(got, want) {
		t.Fatal("wire corruption", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if network.mobile.lost.Load() == 0 || network.mobile.retries.Load() != network.mobile.lost.Load() {
		t.Fatal("missing retransmissions")
	}
	server.Close()
	peer.Close()
	joined := make(chan struct{})
	go func() { relay.wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("relay leaked goroutines")
	}
}

func TestMobileUnmarkedProviderConnectionHasNoImpairment(t *testing.T) {
	network := newProviderTestNetwork(0)
	network.mobileEnabled = true
	network.mobile.set(mobileLinkProfile{RTT: time.Second, Mbps: 1, LossPercent: 5, Seed: 1})
	peer, wire := net.Pipe()
	server := network.wrap("a01", wire)
	defer server.Close()
	defer peer.Close()
	_ = server.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	go func() { peer.Write([]byte("x")) }()
	var got [1]byte
	if _, err := io.ReadFull(server, got[:]); err != nil || got[0] != 'x' {
		t.Fatal("server-side connection impaired", err)
	}
	if network.mobile.segments.Load() != 0 {
		t.Fatal("server-side bytes counted as mobile")
	}
}

func TestMobileBaselineNoopScopeAndCancellation(t *testing.T) {
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	f := newProductionSupervisorFixture(t)
	f.network.mobile.set(mobileLinkProfile{RTT: 100 * time.Millisecond, Mbps: 600})
	c, err := f.baselineDial(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if !f.network.mobileEnabled {
		t.Fatal("fixture mobile scope disabled")
	}
	for i := 0; i < 3; i++ {
		rtt, err := c.Ping(f.ctx)
		if err != nil || rtt < 90*time.Millisecond || rtt > 160*time.Millisecond {
			t.Fatal("baseline observation", i, rtt, err, "segments", f.network.mobile.segments.Load(), "client", c.LocalAddr().String())
		}
	}
	f.network.mobile.set(mobileLinkProfile{RTT: 300 * time.Millisecond, Mbps: 600})
	ctx, cancel := context.WithTimeout(f.ctx, 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = c.Ping(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 150*time.Millisecond {
		t.Fatal("unbounded/cancelled NOOP", err, time.Since(started))
	}
}

func checkMobileScopeUsesBothSocketEndpoints(t *testing.T) {
	network := newProviderTestNetwork(0)
	// Different provider destinations may use the same client-side address.
	// Marking by that address alone can accidentally impair the server role.
	a := &providerTestConn{originalPeer: "127.0.0.1:41000", originalLocal: "127.0.0.1:9931"}
	b := &providerTestConn{originalPeer: "127.0.0.1:41000", originalLocal: "127.0.0.1:9932"}
	network.active[1], network.active[2] = a, b
	if !network.markMobileClient(a.originalPeer, a.originalLocal) || !a.mobileClient.Load() || b.mobileClient.Load() {
		t.Fatal("cross-provider role marked")
	}
	if network.markMobileClient(a.originalPeer, "") || network.markMobileClient(a.originalPeer, "127.0.0.1:9933") {
		t.Fatal("incomplete or foreign socket tuple accepted")
	}
	b.closed.Store(true)
	if network.markMobileClient(b.originalPeer, b.originalLocal) {
		t.Fatal("closed provider socket marked")
	}
}

func TestMobileLargeAppendUsesAdvertisedLiteralCapability(t *testing.T) {
	for _, mode := range []struct {
		name, plus string
		largeTurns int
	}{{"minus", "0", 2}, {"plus", "1", 1}} {
		t.Run(mode.name, func(t *testing.T) {
			t.Setenv("ITOM_LOAD_LITERAL_PLUS", mode.plus)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			network := newProviderTestNetwork(0)
			trace := &literalBudgetTrace{}
			network.debugWriter = trace
			network.mobileEnabled = true
			cm, _ := tlsMailPairWithNetwork(t, ctx, []string{"latency-budget"}, network, "a01")
			c := cm.(*laneMail).Client
			if !network.markMobileClient(c.LocalAddr().String(), c.RemoteAddr().String()) {
				t.Fatal("socket scope")
			}
			network.mobile.set(mobileLinkProfile{RTT: 100 * time.Millisecond, Mbps: 600})
			for _, sample := range []struct{ bodyBytes, turns int }{{100, 1}, {32 << 10, mode.largeTurns}} {
				raw, err := mailbox.Encode(make([]byte, sample.bodyBytes))
				if err != nil {
					t.Fatal(err)
				}
				trace.reset()
				start := time.Now()
				err = c.Append(ctx, "latency-budget", raw)
				elapsed := time.Since(start)
				expected := time.Duration(sample.turns) * 100 * time.Millisecond
				// Count actual protocol turns; host scheduling and TLS record splitting
				// cannot reliably be distinguished from an extra handshake by elapsed time.
				rawTrace := trace.snapshot()
				markers := regexp.MustCompile(`\{([0-9]+)(\+?)\}\r\n`).FindAllStringSubmatch(rawTrace, -1)
				continuations := len(regexp.MustCompile(`(?m)^\+ [^\r\n]*\r\n`).FindAllString(rawTrace, -1))
				nonSync := mode.plus == "1" || len(raw) <= 4096
				if len(markers) != 1 || markers[0][1] != fmt.Sprint(len(raw)) || (markers[0][2] == "+") != nonSync || continuations != sample.turns-1 {
					t.Fatalf("literal protocol shape: markers=%d continuation_count=%d wanted_turns=%d", len(markers), continuations, sample.turns)
				}
				if err != nil || elapsed < expected*9/10 || elapsed >= time.Second {
					t.Fatal("literal round-trip budget", sample, elapsed, err)
				}
				t.Logf("literal_budget capability=%s raw_body_bytes=%d mime_bytes=%d round_trips=%d elapsed_ns=%d protocol_shape_verified=true", mode.name, sample.bodyBytes, len(raw), sample.turns, elapsed.Nanoseconds())
			}
		})
	}
}

func checkMobileByteClockDoesNotChargeLateWakePerTLSRecord(t *testing.T) {
	arrival := time.Now().Add(-time.Second)
	rate := providerRateLane{mbps: 600}
	const bytes = 16406
	const records = 512
	for i := 0; i < records; i++ {
		if !rate.waitUntilAt(bytes, arrival, nil) {
			t.Fatal("uncancelled byte clock stopped")
		}
	}
	mbps := float64(600)
	service := time.Duration(float64(bytes*8) / (mbps * 1e6) * float64(time.Second))
	if got, want := rate.next.Sub(arrival), records*service; got != want {
		t.Fatal("late consumer introduced serialization debt", got, want)
	}
}

func TestMobileFullFetchPageDoesNotAcquireArtificialFlightWindow(t *testing.T) {
	network := newProviderTestNetwork(0)
	network.mobileEnabled = true
	network.mobile.set(mobileLinkProfile{RTT: 200 * time.Millisecond, Mbps: 600})
	peer, wire := net.Pipe()
	server := network.wrap("a01", wire).(*providerTestConn)
	server.mobileClient.Store(true)
	defer server.Close()
	defer peer.Close()
	_ = server.SetReadDeadline(time.Now().Add(3 * time.Second))
	// Sixteen 512KiB MIME bodies are the maximum ordinary bounded FETCH page.
	// TLS-sized writes previously consumed the relay's queue before propagation
	// completed, imposing a false per-connection bandwidth ceiling.
	want := bytes.Repeat([]byte{0x5a}, 8<<20)
	finished := make(chan error, 1)
	start := time.Now()
	go func() {
		for offset := 0; offset < len(want); offset += 16406 {
			if _, err := peer.Write(want[offset:min(offset+16406, len(want))]); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(server, got); err != nil || !bytes.Equal(want, got) {
		t.Fatal("fetch page wire integrity", err)
	}
	elapsed := time.Since(start)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	ideal := 100*time.Millisecond + time.Duration(float64(len(want)*8)/600e6*float64(time.Second))
	// Independent propagation + serialization budget; 50ms bounds local relay
	// scheduling/copy work. The 10% allowance applies to the network component.
	// Race instrumentation changes local copy/scheduling cost. Enforce the
	// performance ceiling only in the dedicated non-race calibration run;
	// ordinary/race tests still enforce bytes, propagation and the deadline.
	if elapsed < 90*time.Millisecond || (os.Getenv("ITOM_NETWORK_CALIBRATION") == "1" && elapsed > ideal*11/10+50*time.Millisecond) {
		t.Fatal("artificial relay flight-window limit", elapsed, ideal)
	}
	t.Logf("mobile_calibration bytes=%d tls_write_bytes=16406 baseline_ms=200 cap_mbps=600 elapsed_ns=%d ideal_network_ns=%d", len(want), elapsed.Nanoseconds(), ideal.Nanoseconds())
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioMobileNetworkRegression(t *testing.T) {
	t.Log("check=checkMobileScopeUsesBothSocketEndpoints")
	checkMobileScopeUsesBothSocketEndpoints(t)
	t.Log("check=checkMobileByteClockDoesNotChargeLateWakePerTLSRecord")
	checkMobileByteClockDoesNotChargeLateWakePerTLSRecord(t)
}

// Keep fake-provider wire diagnostics in memory only. Authentication/MIME bytes
// are never printed or included in evidence; tests report only verified counts.
type literalBudgetTrace struct {
	mu   sync.Mutex
	body bytes.Buffer
}

func (t *literalBudgetTrace) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.body.Write(p)
}
func (t *literalBudgetTrace) reset() { t.mu.Lock(); defer t.mu.Unlock(); t.body.Reset() }
func (t *literalBudgetTrace) snapshot() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.body.String()
}
