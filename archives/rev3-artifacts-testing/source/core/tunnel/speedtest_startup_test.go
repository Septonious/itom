package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"itom.local/core/wire"
)

// A Speedtest-like launch workload, not a reimplementation of Ookla's private
// protocol. Unlike the steady-state gate, every round starts cold connections
// together, completes TLS/configuration, then sends repeated short exchanges.
// The provider, access shaper, journals and supervisors are the production path.
func TestSpeedtestParallelColdStartup(t *testing.T) {
	if os.Getenv("ITOM_SPEEDTEST_STARTUP") != "1" {
		t.Skip("set ITOM_SPEEDTEST_STARTUP=1 for cold parallel application startup")
	}
	if raceInstrumented {
		t.Fatal("measure startup without race instrumentation")
	}
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", "200")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "1")
	t.Setenv("ITOM_FIELD_PROVIDER_MODEL", "1")
	t.Setenv("ITOM_FIELD_LATENCY_PROFILE", "service")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", "10")
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	rtt := loadEnvInt(t, "ITOM_SPEEDTEST_STARTUP_RTT_MS", 50, 0, 300)
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", strconv.Itoa(rtt))
	f := newProductionSupervisorFixtureAccounts(t, 10)
	carriers := snapshotLoadCarriers(t, f, 10, 10)
	if os.Getenv("ITOM_FIELD_HETEROGENEOUS") == "1" {
		deadline := time.Now().Add(15 * time.Second)
		for {
			measured := 0
			f.client.mu.Lock()
			for _, l := range f.client.lanes {
				if l.rttAt.Load() > 0 {
					measured++
				}
			}
			f.client.mu.Unlock()
			if measured == 10 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("missing idle echo measurements before heterogeneous startup")
			}
			if err := pause(f.ctx, 20*time.Millisecond); err != nil {
				t.Fatal(err)
			}
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "configuration-ready")
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(port)
		if err != nil {
			return nil, err
		}
		c, _, err := f.client.Open(ctx, wire.Connect{Host: host, Port: uint16(n)})
		if err != nil {
			return nil, err
		}
		return c.(net.Conn), nil
	}
	// Echo uses an 80-byte setup exchange, as seen in the retained port-8080
	// traces. Opaque application payloads are never inferred from trace hashes.
	echoAddress := f.listener.Addr().String()
	for round, count := range []int{1, 8, 24, 24} {
		tr := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true, MaxConnsPerHost: count}
		client := &http.Client{Transport: tr, Timeout: 3300 * time.Millisecond}
		if rtt >= 200 {
			client.Timeout = 5 * time.Second
		}
		type result struct {
			kind     string
			duration time.Duration
			err      error
		}
		results := make(chan result, count*2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < count; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				begin := time.Now()
				response, err := client.Get(server.URL)
				if err == nil {
					body, e := io.ReadAll(response.Body)
					response.Body.Close()
					err = e
					if err == nil && string(body) != "configuration-ready" {
						err = fmt.Errorf("configuration integrity")
					}
				}
				results <- result{"https", time.Since(begin), err}
			}()
			go func(index int) {
				defer wg.Done()
				<-start
				begin := time.Now()
				ctx, cancel := context.WithTimeout(f.ctx, client.Timeout)
				defer cancel()
				conn, err := dial(ctx, "tcp", echoAddress)
				if err == nil {
					defer conn.Close()
					for turn := 0; turn < 5 && err == nil; turn++ {
						payload := bytes.Repeat([]byte{byte(index + turn)}, 80)
						received := make([]byte, len(payload))
						deadline := time.Now().Add(time.Second)
						if rtt >= 200 {
							deadline = time.Now().Add(1500 * time.Millisecond)
						}
						conn.SetDeadline(deadline)
						_, err = conn.Write(payload)
						if err == nil {
							_, err = io.ReadFull(conn, received)
						}
						if err == nil && !bytes.Equal(payload, received) {
							err = fmt.Errorf("setup exchange integrity")
						}
					}
				}
				results <- result{"tcp-five-turns", time.Since(begin), err}
			}(i)
		}
		close(start)
		wg.Wait()
		close(results)
		tr.CloseIdleConnections()
		for res := range results {
			t.Logf("cold_start round=%d connections_per_kind=%d kind=%s duration_ms=%d added_access_rtt_ms=%d error=%v", round+1, count, res.kind, res.duration.Milliseconds(), rtt, res.err)
			if res.err != nil {
				t.Error(res.err)
			}
		}
		assertLoadCarriersPreserved(t, f, carriers)
		if t.Failed() {
			fieldLogLoadState(t, f, "cold-start")
			return
		}
	}
}

func TestSpeedtestHeterogeneousIdleAndParallelStartup(t *testing.T) {
	if os.Getenv("ITOM_SPEEDTEST_HETEROGENEOUS") != "1" {
		t.Skip("set ITOM_SPEEDTEST_HETEROGENEOUS=1 for uneven-account startup")
	}
	t.Setenv("ITOM_SPEEDTEST_STARTUP", "1")
	t.Setenv("ITOM_SPEEDTEST_STARTUP_RTT_MS", "100")
	t.Setenv("ITOM_FIELD_HETEROGENEOUS", "1")
	TestSpeedtestParallelColdStartup(t)
}

// Sequential download -> upload -> duplex, cold bulk TLS connections per phase,
// three consecutive rounds and fixed useful-byte/tail accounting. This shares
// the existing 50 Mbps and <1000 ms gates without changing their thresholds.
func TestSpeedtest200MbpsApplicationPhases(t *testing.T) {
	if os.Getenv("ITOM_SPEEDTEST_200") != "1" {
		t.Skip("set ITOM_SPEEDTEST_200=1 for 200 Mbps application phases")
	}
	t.Setenv("ITOM_FIELD_LOAD", "1")
	t.Setenv("ITOM_REWORK_STRICT", "1")
	t.Setenv("ITOM_FIELD_ACCESS_MBPS", "200")
	t.Setenv("ITOM_FIELD_130_RTT_MS", "50")
	t.Setenv("ITOM_FIELD_STREAMS", "16")
	t.Setenv("ITOM_FIELD_SECONDS", "30")
	t.Setenv("ITOM_FIELD_REPEATS", "3")
	t.Setenv("ITOM_FIELD_WARMUP_SECONDS", "10")
	fieldApplicationMeasurement(t, true)
}
