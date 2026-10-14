package tunnel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"itom.local/core/wire"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fieldPatternReader struct{ pattern *productionPattern }

func (r fieldPatternReader) Read(b []byte) (int, error) { r.pattern.fill(b); return len(b), nil }

// Real HTTPS payloads through actual supervisors, IMAP/TLS, durable journals
// and sockets. Physical shaping is calibrated separately; assumed provider
// service and recorded wrapper-duration stress are distinct profiles.
func TestTransport20ApplicationAcceptance(t *testing.T) {
	fieldApplicationMeasurement(t, false)
}

// Measurement, deliberately not a 20 Mbps acceptance gate. A 130 Mbps access
// shaper says nothing about real provider service or physical path RTT.
func TestTransport130ApplicationMeasurement(t *testing.T) {
	if os.Getenv("ITOM_FIELD_130_LOAD") != "1" {
		t.Skip("set ITOM_FIELD_130_LOAD=1 for a separate 130 Mbps measurement")
	}
	t.Setenv("ITOM_FIELD_LOAD", "1")
	fieldApplicationMeasurement(t, true)
}

func fieldApplicationMeasurement(t *testing.T, measure130 bool) {
	if os.Getenv("ITOM_FIELD_LOAD") != "1" {
		t.Skip("set ITOM_FIELD_LOAD=1 for the field-shaped HTTPS gate")
	}
	if raceInstrumented {
		t.Fatal("run speed gate without race instrumentation")
	}
	t.Setenv("ITOM_LOAD_MOBILE", "1")
	mobile := os.Getenv("ITOM_FIELD_MOBILE") == "1"
	if measure130 {
		mobile = false
	}
	// Provider replay and physical access shaping are independent dimensions.
	// The 20 Mbps model must not silently disable provider latency/faults.
	providerModel := "1"
	t.Setenv("ITOM_FIELD_PROVIDER_MODEL", providerModel)
	profile := os.Getenv("ITOM_FIELD_LATENCY_PROFILE")
	if profile == "" {
		profile = "service"
	}
	if profile != "recorded" && profile != "median" && profile != "service" {
		t.Fatal("unknown field latency profile", profile)
	}
	t.Setenv("ITOM_FIELD_LATENCY_PROFILE", profile)
	seconds := loadEnvInt(t, "ITOM_FIELD_SECONDS", 60, 15, 60)
	streams := loadEnvInt(t, "ITOM_FIELD_STREAMS", 4, 1, 32)
	phases := []string{"download", "upload", "duplex"}
	repeats := loadEnvInt(t, "ITOM_FIELD_REPEATS", 1, 1, 3)
	if only := os.Getenv("ITOM_FIELD_PHASE"); only != "" {
		if only != "download" && only != "upload" && only != "duplex" {
			t.Fatal("invalid phase", only)
		}
		phases = []string{only}
	}
	t.Logf("field_encoding append=base64 binary_fetch_advertised=%t streams_per_direction=%d", os.Getenv("ITOM_LOAD_BINARY_FETCH") == "1", streams)
	backgroundStreams := loadEnvInt(t, "ITOM_FIELD_BACKGROUND_STREAMS", 0, 0, 40)
	warmupSeconds := loadEnvInt(t, "ITOM_FIELD_WARMUP_SECONDS", 10, 0, 30)
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", "250")
	accessMbps := 130
	accessRTT := 250
	if mobile {
		accessMbps = loadEnvInt(t, "ITOM_FIELD_MOBILE_MBPS", 20, 10, 200)
		accessRTT = loadEnvInt(t, "ITOM_FIELD_MOBILE_RTT_MS", 250, 100, 300)

		t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", strconv.Itoa(accessRTT))
	}
	if measure130 {
		accessMbps = 130
		accessRTT = loadEnvInt(t, "ITOM_FIELD_130_RTT_MS", 250, 0, 1000)
	}
	if os.Getenv("ITOM_REWORK_STRICT") == "1" {
		accessMbps = loadEnvInt(t, "ITOM_FIELD_ACCESS_MBPS", 600, 130, 2000)
	}
	t.Setenv("ITOM_LOAD_MOBILE_RTT_MS", strconv.Itoa(accessRTT))
	t.Setenv("ITOM_LOAD_MOBILE_MBPS", strconv.Itoa(accessMbps))
	t.Setenv("ITOM_LOAD_MOBILE_LOSS_PERCENT", "0")
	t.Setenv("ITOM_LOAD_PROVIDER_MBPS", "0")
	commandDelay := "60"
	if mobile || measure130 {
		commandDelay = "1"
	}
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", commandDelay)
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", "10")
	t.Setenv("ITOM_LOAD_DIAGNOSTICS", "1")
	start := time.Now()
	if mobile {
		if profile == "recorded" {
			t.Log("provider_latency=FIELD46 wrapper-duration replay plus independently shaped access; uncalibrated stress model")
		} else {
			t.Log("provider_latency=assumed fixed service delays; synthetic healthy-provider baseline, not empirical qualification")
		}
		t.Logf("field_mobile_model per_direction_access_mbps=%d propagation_rtt_ms=%d loss=0; reliable relay, not kernel TCP/radio/provider emulation", accessMbps, accessRTT)
	} else {
		t.Logf("field_latency_profile=%s access_mbps=%d propagation_rtt_ms=%d provider_costs=assumed_or_wrapper_replay real_provider=NOT_RUN", profile, accessMbps, accessRTT)
	}
	t.Logf("field_read_stalls enabled=%t accounts=a02,a06,a08 delay=recorded_fetch_tail trigger_after_s=20", os.Getenv("ITOM_FIELD_READ_STALLS") == "1")
	if mobile || measure130 {
		calibrateProviderAccess(t, mobileLinkProfile{RTT: time.Duration(accessRTT) * time.Millisecond, Mbps: float64(accessMbps), Seed: 714, SharedMedium: os.Getenv("ITOM_LOAD_MOBILE_SHARED_MEDIUM") == "1"})
	}
	start = time.Now()
	f := newProductionSupervisorFixtureState(t, 10, fieldLegacyState(t))
	t.Logf("field_startup elapsed_ms=%d installed_profile=8MiB accounts=10 lost_publications=3 access_mbps=%d added_rtt_ms=%d", time.Since(start).Milliseconds(), accessMbps, accessRTT)
	if time.Since(start) > 30*time.Second {
		t.Fatal("startup exceeded30s before any application traffic")
	}
	var down atomic.Int64
	var phaseRegistry httpPhaseRegistry
	failures := make(chan error, 32)
	fail := func(err error) {
		if err != nil {
			select {
			case failures <- err:
			default:
			}
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := r.URL.Query().Get("id")
		p := newProductionPattern(identity)
		buf, scratch := make([]byte, 32<<10), make([]byte, 32<<10)
		switch r.URL.Path {
		case "/configuration", "/ping":
			_, _ = io.WriteString(w, identity)
		case "/download":
			for r.Context().Err() == nil {
				p.fill(buf)
				if _, err := w.Write(buf); err != nil {
					return
				}
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
			}
		case "/upload":
			phaseMeasurement := phaseRegistry.get(strings.SplitN(identity, "/", 2)[0])
			if phaseMeasurement == nil {
				http.Error(w, "unknown run", http.StatusBadRequest)
				return
			}
			for {
				n, err := r.Body.Read(buf)
				if n > 0 {
					if e := p.verify(buf[:n], scratch); e != nil {
						fail(e)
						return
					}
					phaseMeasurement.up.Add(int64(n))
					phaseMeasurement.firstPayload(identity, time.Now())
					phaseMeasurement.window.add(true, n, time.Now())
				}
				if err != nil {
					if phaseMeasurement.active.Load() {
						fail(fmt.Errorf("upload body stopped id=%s: %w", identity, err))
					}
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer func() {
		// A failed bulk request can leave its remote handler waiting for the
		// tunnel. Close sockets before waiting, so failure diagnostics and
		// supervisor cleanup still run instead of hanging in Server.Close.
		server.CloseClientConnections()
		server.Close()
	}()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	dial := func(ctx context.Context, _ string, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
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
		conn, ok := c.(net.Conn)
		if !ok {
			c.Close()
			return nil, fmt.Errorf("application connection %T", c)
		}
		return conn, nil
	}
	newHTTP := func(timeout time.Duration) *http.Client {
		tr := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: roots}, MaxConnsPerHost: 2 * streams, ForceAttemptHTTP2: false}
		t.Cleanup(tr.CloseIdleConnections)
		return &http.Client{Transport: tr, Timeout: timeout}
	}
	for i := 0; i < 3; i++ {
		coldTimeout := 3300 * time.Millisecond
		if mobile {
			coldTimeout = 5 * time.Second
		}
		coldClient := newHTTP(coldTimeout)
		coldClient.Transport.(*http.Transport).DisableKeepAlives = true
		begin := time.Now()
		var connected, tlsBegin, tlsDone, firstByte atomic.Int64
		request, _ := http.NewRequestWithContext(f.ctx, http.MethodGet, server.URL+"/configuration?id=field-config", nil)
		trace := &httptrace.ClientTrace{
			GotConn:              func(httptrace.GotConnInfo) { connected.Store(time.Since(begin).Nanoseconds()) },
			TLSHandshakeStart:    func() { tlsBegin.Store(time.Since(begin).Nanoseconds()) },
			TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsDone.Store(time.Since(begin).Nanoseconds()) },
			GotFirstResponseByte: func() { firstByte.Store(time.Since(begin).Nanoseconds()) },
		}
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
		response, err := coldClient.Do(request)
		if err != nil {
			t.Fatal("cold HTTPS startup", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "field-config" {
			t.Fatal("cold HTTPS response", err)
		}
		t.Logf("field_cold_https index=%d duration_ms=%d open_and_tls_ms=%d tls_ms=%d ttfb_ms=%d", i+1, time.Since(begin).Milliseconds(), connected.Load()/1e6, (tlsDone.Load()-tlsBegin.Load())/1e6, firstByte.Load()/1e6)
	}
	// Probe launches are independent, so RTT >1s overlaps requests. Prime a
	// bounded pool of SIX dedicated HTTP/1 TLS connections before load; warming
	// one connection silently added cold OPEN+TLS to later "warm" probes. Each
	// slot is reused only after 6s, beyond the 5s request deadline. Cold startup
	// remains measured above, and every loaded request retains its full RTT/error.
	probeTimeout := 5 * time.Second
	rttLimit := time.Second
	if mobile {
		rttLimit = 1500 * time.Millisecond
	}
	probeClients := make([]*http.Client, 6)
	warmErrors := make(chan error, len(probeClients))
	var warmWG sync.WaitGroup
	for i := range probeClients {
		client := newHTTP(probeTimeout)
		tr := client.Transport.(*http.Transport)
		tr.MaxConnsPerHost, tr.MaxIdleConnsPerHost = 1, 1
		probeClients[i] = client
		warmWG.Add(1)
		go func() {
			defer warmWG.Done()
			response, err := client.Get(server.URL + "/ping?id=probe-init")
			if err == nil {
				data, e := io.ReadAll(response.Body)
				response.Body.Close()
				err = e
				if err == nil && string(data) != "probe-init" {
					err = fmt.Errorf("probe warmup integrity")
				}
			}
			if err != nil {
				warmErrors <- err
			}
		}()
	}
	warmWG.Wait()
	close(warmErrors)
	for err := range warmErrors {
		t.Fatal("probe TLS pool warmup", err)
	}
	t.Logf("field_probe_pool connections=%d timeout_ms=%d cold_startup_separate=true", len(probeClients), probeTimeout.Milliseconds())
	if measure130 {
		for i := 0; i < 4; i++ {
			if err := pause(f.ctx, 1100*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			begin := time.Now()
			var reused bool
			request, _ := http.NewRequestWithContext(f.ctx, http.MethodGet, server.URL+"/ping?id=idle-probe", nil)
			request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
			response, err := probeClients[0].Do(request)
			if err == nil {
				body, e := io.ReadAll(response.Body)
				response.Body.Close()
				err = e
				if string(body) != "idle-probe" && err == nil {
					err = fmt.Errorf("idle probe integrity")
				}
			}
			t.Logf("field_idle_http_probe index=%d access_mbps=%d propagation_rtt_ms=%d idle_gap_ms=1100 rtt_ms=%d tls_reused=%t error=%v", i+1, accessMbps, accessRTT, time.Since(begin).Milliseconds(), reused, err)
			if err != nil || !reused {
				t.Fatal("idle application probe failed", err)
			}
		}
	}
	stressCarriers := snapshotLoadCarriers(t, f, 10, 10)
	stressLogical, stressGeneration := f.client.logicalSession, f.client.generation
	expanded := []string{}
	for i := 0; i < repeats; i++ {
		expanded = append(expanded, phases...)
	}
	for phaseIndex, phase := range expanded {
		t.Logf("rework_http_run repeat=%d phase=%s same_transport=true", phaseIndex/len(phases)+1, phase)
		phaseKey := fmt.Sprintf("run%d-%s", phaseIndex+1, phase)
		phaseStarted := time.Now()
		phaseMeasurement := phaseRegistry.start(phaseKey, phaseStarted.Add(time.Duration(warmupSeconds)*time.Second), time.Duration(seconds)*time.Second)
		window := phaseMeasurement.window
		ctx, cancel := context.WithCancel(f.ctx)
		var wg sync.WaitGroup
		for stream := 0; stream < backgroundStreams; stream++ {
			wg.Add(1)
			go func(stream int) {
				defer wg.Done()
				browser := newHTTP(5 * time.Second)
				defer browser.CloseIdleConnections()
				identity := fmt.Sprintf("browser-%d", stream)
				for ctx.Err() == nil {
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/ping?id="+identity, nil)
					response, err := browser.Do(req)
					if err == nil {
						data, e := io.ReadAll(response.Body)
						response.Body.Close()
						err = e
						if err == nil && string(data) != identity {
							err = fmt.Errorf("browser integrity")
						}
					}
					if ctx.Err() != nil {
						return
					}
					if err != nil {
						fail(fmt.Errorf("browser stream %d: %w", stream, err))
						return
					}
					if pause(ctx, 2*time.Second) != nil {
						return
					}
				}
			}(stream)
		}
		workerClient := newHTTP(0)
		for i := 0; i < streams; i++ {
			for _, direction := range []string{"upload", "download"} {
				if phase != "duplex" && phase != direction {
					continue
				}
				id := fmt.Sprintf("%s/%s/%d", phaseKey, direction, i)
				wg.Add(1)
				go func(direction, id string) {
					defer wg.Done()
					method := http.MethodGet
					var body io.Reader
					if direction == "upload" {
						method = http.MethodPost
						body = fieldPatternReader{newProductionPattern(id)}
					}
					r, err := http.NewRequestWithContext(ctx, method, server.URL+"/"+direction+"?id="+id, body)
					if err != nil {
						fail(err)
						return
					}
					response, err := workerClient.Do(r)
					if err != nil {
						if ctx.Err() == nil {
							fail(err)
						}
						return
					}
					defer response.Body.Close()
					if direction == "upload" && ctx.Err() == nil {
						fail(fmt.Errorf("upload ended before measurement deadline id=%s status=%s", id, response.Status))
						return
					}
					if direction == "download" {
						buf, scratch := make([]byte, 32<<10), make([]byte, 32<<10)
						pattern := newProductionPattern(id)
						for {
							n, err := response.Body.Read(buf)
							if n > 0 {
								if e := pattern.verify(buf[:n], scratch); e != nil {
									fail(e)
									return
								}
								down.Add(int64(n))
								phaseMeasurement.firstPayload(id, time.Now())
								window.add(false, n, time.Now())
							}
							if err != nil {
								if ctx.Err() == nil {
									fail(err)
								}
								return
							}
						}
					}
				}(direction, id)
			}
		}

		if err := pause(ctx, time.Until(window.begin)); err != nil {
			t.Fatal(err)
		}
		type probeResult struct {
			index  int
			lag    time.Duration
			rtt    time.Duration
			reused bool
			err    error
		}
		results := make(chan probeResult, seconds)
		var captureFailure sync.Once
		var probeWG sync.WaitGroup
		// Launch probes on an independent clock. A slow probe neither blocks
		// the next launch nor extends the useful-byte measurement interval.
		for second := 0; second < seconds; second++ {
			index := second + 1
			probeWG.Add(1)
			go func() {
				defer probeWG.Done()
				started := time.Now()
				var reused atomic.Bool
				request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/ping?id=loaded-probe", nil)
				request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }}))
				response, err := probeClients[(index-1)%len(probeClients)].Do(request)
				if err == nil {
					body, e := io.ReadAll(response.Body)
					response.Body.Close()
					err = e
					if string(body) != "loaded-probe" && err == nil {
						err = fmt.Errorf("probe integrity")
					}
				}
				lag := started.Sub(window.begin.Add(time.Duration(index-1) * time.Second))
				if lag > 250*time.Millisecond && err == nil {
					err = fmt.Errorf("probe scheduled late by %s", lag)
				}
				if err == nil && !reused.Load() {
					err = fmt.Errorf("warm probe lost its established TLS connection")
				}
				if os.Getenv("ITOM_LOAD_STACKS") == "1" && err != nil && ctx.Err() == nil {
					// Capture while bulk is still running; canceling first destroys
					// the blocked writer/reader state that explains a repeated-run stall.
					captureFailure.Do(func() {
						buf := make([]byte, 1<<20)
						n := runtime.Stack(buf, true)
						t.Logf("application_failure_stacks phase=%s bytes=%d\n%s", phase, n, buf[:n])
					})
				}
				results <- probeResult{index, lag, time.Since(started), reused.Load(), err}
			}()
			if delay := time.Until(window.begin.Add(time.Duration(second+1) * time.Second)); delay > 0 {
				if err := pause(ctx, delay); err != nil {
					t.Error(err)
					break
				}
			}
		}
		// Byte accounting closes at window.end regardless of this join. Keep
		// bulk active while joining probes launched inside the window: stopping
		// it first would mix a RESET/FIN teardown burst into the last load probe.
		// Late probes still contribute their full RTT/error and time out normally;
		// bytes after the fixed deadline never count toward useful throughput.
		probeWG.Wait()
		phaseMeasurement.active.Store(false)
		cancel()
		workerClient.CloseIdleConnections()
		wg.Wait()
		close(results)
		var maxRTT time.Duration
		var probeFailure error
		probeCount, probeErrors := 0, 0
		for result := range results {
			probeCount++
			maxRTT = max(maxRTT, result.rtt)
			t.Logf("field_http_probe phase=%s index=%d rtt_ms=%d launch_lag_ms=%d tls_reused=%t error=%v", phase, result.index, result.rtt.Milliseconds(), result.lag.Milliseconds(), result.reused, result.err)
			if result.err != nil {
				probeErrors++
				probeFailure = result.err
			}
		}
		select {
		case err := <-failures:
			probeFailure = err
			probeErrors++
		default:
		}
		fieldLogLoadState(t, f, phase)
		startupOK := true
		if os.Getenv("ITOM_SPEEDTEST_200") == "1" {
			count, slowest := phaseMeasurement.startupSummary(phaseStarted)
			expected := streams
			if phase == "duplex" {
				expected *= 2
			}
			t.Logf("application_startup phase=%s streams_started=%d expected=%d slowest_first_payload_ms=%d deadline_ms=5000", phase, count, expected, slowest.Milliseconds())
			if count != expected || slowest > 5*time.Second {
				startupOK = false
				t.Errorf("parallel bulk startup failed: %d/%d first payload max=%s", count, expected, slowest)
			}
		}
		upMbps, downMbps, tailUpMbps, tailDownMbps := window.rates()
		qualified := startupOK && probeFailure == nil && transport20Accepted(upMbps, downMbps, tailUpMbps, tailDownMbps, phase != "download", phase != "upload", maxRTT, probeCount, probeErrors)
		if probeCount != seconds {
			qualified = false
			probeFailure = fmt.Errorf("missing application probes: %d/%d", probeCount, seconds)
		}
		qualification := "NOT_ASSESSED"
		if os.Getenv("ITOM_REWORK_STRICT") == "1" {
			qualification = "PASS"
			if !startupOK || probeFailure != nil || !reworkHTTPAccepted(upMbps, downMbps, tailUpMbps, tailDownMbps, phase != "download", phase != "upload", maxRTT, probeCount, seconds, probeErrors) {
				qualification = "FAIL"
				t.Errorf("50-Mbps application qualification failed: phase=%s up=%.3f down=%.3f tail_up=%.3f tail_down=%.3f max_rtt=%s probes=%d errors=%d", phase, upMbps, downMbps, tailUpMbps, tailDownMbps, maxRTT, probeCount, probeErrors)
			}
		}
		if measure130 {
			t.Logf("field_130_result phase=%s measured_ms=%d warmup_s=%d streams_per_direction=%d background_streams=%d upload_mbps=%.3f download_mbps=%.3f tail_upload_mbps=%.3f tail_download_mbps=%.3f max_rtt_ms=%d probes=%d probe_errors=%d access_mbps=%d propagation_rtt_ms=%d qualification=%s real_android_provider=NOT_RUN failure=%v", phase, window.end.Sub(window.begin).Milliseconds(), warmupSeconds, streams, backgroundStreams, upMbps, downMbps, tailUpMbps, tailDownMbps, maxRTT.Milliseconds(), probeCount, probeErrors, accessMbps, accessRTT, qualification, probeFailure)
		} else {
			t.Logf("field_https_result phase=%s measured_ms=%d warmup_s=%d upload_mbps=%.3f download_mbps=%.3f tail_upload_mbps=%.3f tail_download_mbps=%.3f max_rtt_ms=%d probes=%d probe_errors=%d model20_criteria_met=%t real_android_provider=NOT_RUN failure=%v", phase, window.end.Sub(window.begin).Milliseconds(), warmupSeconds, upMbps, downMbps, tailUpMbps, tailDownMbps, maxRTT.Milliseconds(), probeCount, probeErrors, qualified, probeFailure)
		}
		if mobile && !qualified {
			t.Error("20 Mbps model qualification failed: useful direction/tail >=5 Mbps, application RTT <=1500 ms, zero errors required")
		}

		if probeFailure != nil {
			for _, detail := range f.carrierExits.failureDetails() {
				t.Logf("field_failure_carrier %v", detail)
			}
			t.Error(probeFailure)
		}
		if mobile && maxRTT > rttLimit {
			t.Error("mobile HTTP RTT exceeds user limit1500ms")
		}
		speedLimit := 75.0
		if mobile {
			speedLimit = 5.0
			t.Log("mobile_model_goal user minimum5Mbps useful rate / maximum1500ms RTT; no allowance")
		}
		if !measure130 && (phase != "download" && (upMbps < speedLimit || tailUpMbps < speedLimit) || phase != "upload" && (downMbps < speedLimit || tailDownMbps < speedLimit)) {
			t.Errorf("direction/tail goal failed: minimum%.1fMbps", speedLimit)
		}
		if f.carrierExits.unexpected.Load() != 0 {
			t.Fatal("spontaneous carrier exit")
		}
	}
	if os.Getenv("ITOM_REWORK_STRICT") == "1" {
		for i, client := range probeClients {
			if err := pause(f.ctx, 1100*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			var reused bool
			identity := fmt.Sprintf("post-stress-%d", i)
			request, _ := http.NewRequestWithContext(f.ctx, http.MethodGet, server.URL+"/ping?id="+identity, nil)
			request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("post-stress HTTPS", err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			rtt := time.Since(started)
			if err != nil || string(body) != identity || !reused || rtt >= time.Second {
				t.Fatal("post-stress HTTPS stability", err, reused, rtt)
			}
			t.Logf("field_post_stress index=%d tls_reused=true idle_gap_ms=1100 rtt_ms=%d", i+1, rtt.Milliseconds())
		}
		if f.client.logicalSession != stressLogical || f.client.generation != stressGeneration {
			t.Fatal("post-stress logical session replaced")
		}
		assertLoadCarriersPreserved(t, f, stressCarriers)
	}
	if os.Getenv("ITOM_FIELD_READ_STALLS") == "1" {
		for account, flag := range f.network.fieldLatency.stallAccounts {
			t.Logf("field_read_stall account=%s injected=%t", account, flag.Load())
			if !flag.Load() {
				t.Error("required successful FETCH stall not injected", account)
			}
		}
	}
	t.Logf("field_provider accepted=%d dropped=%d unexpected_carrier_exits=%d", f.network.accepted.Load(), f.network.dropped.Load(), f.carrierExits.unexpected.Load())
}

func fieldLogLoadState(t *testing.T, f *productionSupervisorFixture, phase string) {
	t.Helper()
	f.client.mu.Lock()
	client := make([]*diagnostic, 0, len(f.client.diagnostics))
	for _, d := range f.client.diagnostics {
		client = append(client, d)
	}
	f.client.mu.Unlock()
	f.mu.Lock()
	server := make([]*diagnostic, 0, len(f.serverLanes))
	for _, l := range f.serverLanes {
		server = append(server, l.diag)
	}
	f.mu.Unlock()
	for side, lines := range map[string][]*diagnostic{"client": client, "server": server} {
		var s LineDiagnostic
		for _, d := range lines {
			if d == nil {
				continue
			}
			x := d.snapshot()
			t.Logf("field_line_diag phase=%s side=%s account=%s ready=%s held_bytes=%d remote_gc=%d pending=%d handoffs=%d governor=%+v", phase, side, x.Account, x.Stage, x.DataBytes, x.RemoteGC, x.PendingDescriptors, x.ReaderHandoffs, x.LatencyGovernor)
			s.ReaderHandoffs += x.ReaderHandoffs
			s.DataAppendCount += x.DataAppendCount
			s.DataAppendBytes += x.DataAppendBytes
			s.ControlAppendCount += x.ControlAppendCount
			s.ControlAppendBytes += x.ControlAppendBytes
			s.DataPayloadBytes += x.DataPayloadBytes
			s.DataBatchRecords += x.DataBatchRecords
			s.DataBatchCount += x.DataBatchCount
			s.FetchPolls += x.FetchPolls
			s.EmptyFetchPolls += x.EmptyFetchPolls
			s.MaxPumpQueueWaitMS = max(s.MaxPumpQueueWaitMS, x.MaxPumpQueueWaitMS)
			s.MaxStripeWaitMS = max(s.MaxStripeWaitMS, x.MaxStripeWaitMS)
			s.MaxLocalGrantWaitMS = max(s.MaxLocalGrantWaitMS, x.MaxLocalGrantWaitMS)
			s.MaxRemoteGrantWaitMS = max(s.MaxRemoteGrantWaitMS, x.MaxRemoteGrantWaitMS)
		}
		t.Logf("field_load_diag phase=%s side=%s reader_handoffs=%d data_appends=%d data_mime_bytes=%d payload_bytes=%d batches=%d records=%d control_appends=%d control_bytes=%d fetches=%d empty_fetches=%d max_pump_wait_ms=%d max_stripe_wait_ms=%d max_local_grant_wait_ms=%d max_remote_grant_wait_ms=%d", phase, side, s.ReaderHandoffs, s.DataAppendCount, s.DataAppendBytes, s.DataPayloadBytes, s.DataBatchCount, s.DataBatchRecords, s.ControlAppendCount, s.ControlAppendBytes, s.FetchPolls, s.EmptyFetchPolls, s.MaxPumpQueueWaitMS, s.MaxStripeWaitMS, s.MaxLocalGrantWaitMS, s.MaxRemoteGrantWaitMS)
	}
}
