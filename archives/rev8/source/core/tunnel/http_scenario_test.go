package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Application/protocol scenario, not speed qualification. This uses actual
// HTTP/2/TLS, concurrent upload/download, fresh HTTP/1.1 connections for short
// requests, actual supervisors and a connection-limited TLS/IMAP provider.
func TestHTTPMixedTrafficPreservesIntegrityAndCarriers(t *testing.T) {
	t.Run("nominal", func(t *testing.T) { testHTTPMixedTraffic(t, false) })
	t.Run("provider_cap_recovers", func(t *testing.T) { testHTTPMixedTraffic(t, true) })
}

func testHTTPMixedTraffic(t *testing.T, capRecovers bool) {
	limit := "10"
	if capRecovers {
		limit = "9"
	}
	t.Setenv("ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", limit)
	delayMS := loadEnvInt(t, "ITOM_HTTP_COMMAND_DELAY_MS", 15, 0, 5000)
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", strconv.Itoa(delayMS))
	t.Setenv("ITOM_LOAD_VISIBILITY_DELAY_MS", "100")
	f := newProductionSupervisorFixtureAccounts(t, 4)
	logical, generation := f.client.logicalSession, f.client.generation
	snapshots := snapshotLoadCarriers(t, f, 4, 4)
	// Reserve actual authenticated sessions only after activation, so the
	// provider cap cannot be consumed by an optional cleaner instead of DATA.
	// The server DATA workers dial lazily; HTTP scheduling must not decide
	// whether this scenario really exercises a refused DATA connection.
	var pressure []*mailbox.Client
	var deniedBeforeLoad int
	defer func() {
		for _, c := range pressure {
			_ = c.Close()
		}
	}()
	if capRecovers {
		for _, old := range snapshots {
			transport := old.client.mail
			if observed, ok := transport.(*observedMail); ok {
				transport = observed.Transport
			}
			m := transport.(*laneMail)
			// Other DATA/cleanup workers can be dialing during this fixture
			// setup.  A genuine NO [LIMIT] can race with a worker closing its
			// session between our quota check and our attempted login; that
			// does not mean the provider-limit recovery scenario failed.
			// Retry only a *confirmed provider refusal*, with a finite deadline.
			pressureDeadline := time.Now().Add(2 * time.Second)
			for {
				f.network.mu.Lock()
				full := f.network.sessions[old.id] >= 9
				deniedBeforeDial := f.network.sessionDenied
				f.network.mu.Unlock()
				if full {
					break
				}
				c, err := m.dial(f.ctx)
				if err != nil {
					f.network.mu.Lock()
					nowFull := f.network.sessions[old.id] >= 9
					refused := f.network.sessionDenied > deniedBeforeDial
					f.network.mu.Unlock()
					if nowFull {
						break
					}
					if refused && time.Now().Before(pressureDeadline) {
						select {
						case <-time.After(20 * time.Millisecond):
							continue
						case <-f.ctx.Done():
							t.Fatal("fixture ended while reserving provider sessions", f.ctx.Err())
						}
					}
					t.Fatal("could not reserve provider session pressure", old.id, err)
				}
				pressure = append(pressure, c)
			}
		}
		if len(pressure) == 0 {
			f.network.mu.Lock()
			for _, old := range snapshots {
				if f.network.sessions[old.id] < 9 {
					f.network.mu.Unlock()
					t.Fatal("provider pressure was not exercised")
				}
			}
			f.network.mu.Unlock()
		}
		// The production writer is now reserved before optional services.
		// Close one DATA socket and occupy its provider slot externally so
		// the repair encounters a genuine refusal while HTTP remains intact.
		old := snapshots[0]
		m := physicalMail(old.server.mail)
		m.runtime.mu.Lock()
		writer := m.runtime.workerSockets[0]
		m.runtime.mu.Unlock()
		if writer == nil {
			t.Fatal("seeded DATA writer missing")
		}
		writer.Close()
		awaitCapacity(t, f.ctx, func() bool { f.network.mu.Lock(); defer f.network.mu.Unlock(); return f.network.sessions[old.id] < 9 }, "closed DATA session was not released")
		external, err := f.baselineDial(f.ctx)
		if err != nil {
			t.Fatal("external provider pressure", err)
		}
		pressure = append(pressure, external)
		f.network.mu.Lock()
		deniedBeforeLoad = f.network.sessionDenied
		f.network.mu.Unlock()
		for _, old := range snapshots {
			if dataParallelism(old.server.mail) != 1 {
				t.Fatal("server DATA capacity already degraded before HTTP", old.id)
			}
		}
	}
	payload := bytes.Repeat([]byte("HTTP-bulk-payload-"), 128<<10)
	digest := sha256.Sum256(payload)
	var uploads, downloads, short, activeBulk, shortUnderBulk atomic.Int64
	bulkSeen := make(chan struct{}, 4)
	// The provider-cap recovery setup can outlast an entire bulk transfer on
	// fast hosts. Hold the two download responses open until a short request
	// actually completes; otherwise the overlap assertion measures test
	// goroutine scheduling rather than concurrent HTTP traffic.
	shortCompleted := make(chan struct{})
	var completeShortOnce sync.Once
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			bulkSeen <- struct{}{}
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("X-Payload-SHA256", hex.EncodeToString(digest[:]))
			// Publish real bulk before waiting for the interactive request.
			// This forces the new single DATA worker to encounter the cap.
			n, _ := w.Write(payload[:64<<10])
			downloads.Add(int64(n))
			select {
			case <-shortCompleted:
			case <-r.Context().Done():
				return
			}
			n, _ = w.Write(payload[64<<10:])
			downloads.Add(int64(n))
		case "/upload":
			bulkSeen <- struct{}{}
			h := sha256.New()
			n, err := io.Copy(h, io.LimitReader(r.Body, int64(len(payload)+1)))
			if err != nil || n != int64(len(payload)) || !bytes.Equal(h.Sum(nil), digest[:]) {
				http.Error(w, "invalid upload", http.StatusBadRequest)
				return
			}
			uploads.Add(n)
			_, _ = w.Write([]byte("upload-ok"))
		case "/short":
			short.Add(1)
			_, _ = w.Write([]byte(r.URL.Query().Get("token")))
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
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
			return nil, fmt.Errorf("unexpected application connection %T", c)
		}
		return conn, nil
	}
	bulkTransport := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true, MaxConnsPerHost: 4}
	shortTransport := &http.Transport{DialContext: dial, TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true}
	defer bulkTransport.CloseIdleConnections()
	defer shortTransport.CloseIdleConnections()
	bulkClient := &http.Client{Transport: bulkTransport, Timeout: 30 * time.Second}
	shortClient := &http.Client{Transport: shortTransport, Timeout: 30 * time.Second}
	ctx, cancel := context.WithTimeout(f.ctx, 40*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	start := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			activeBulk.Add(1)
			defer activeBulk.Add(-1)
			method, path := http.MethodGet, "/download"
			var body io.Reader
			if i%2 == 1 {
				method, path, body = http.MethodPost, "/upload", bytes.NewReader(payload)
			}
			req, err := http.NewRequestWithContext(ctx, method, server.URL+path, body)
			if err != nil {
				errors <- err
				return
			}
			response, err := bulkClient.Do(req)
			if err != nil {
				errors <- err
				return
			}
			defer response.Body.Close()
			got, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
			if err != nil || response.StatusCode != 200 || response.ProtoMajor != 2 {
				errors <- fmt.Errorf("bulk HTTP/2 status/protocol/read: %d/%d/%v", response.StatusCode, response.ProtoMajor, err)
				return
			}
			if (i%2 == 0 && !bytes.Equal(got, payload)) || (i%2 == 1 && string(got) != "upload-ok") {
				errors <- fmt.Errorf("bulk application bytes mismatched")
			}
		}(i)
	}
	startShort := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case <-startShort:
			case <-ctx.Done():
				errors <- ctx.Err()
				return
			}
			wasUnderBulk := activeBulk.Load() > 0
			token := fmt.Sprintf("short-%d", i)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/short?token="+token, nil)
			if err != nil {
				errors <- err
				return
			}
			response, err := shortClient.Do(req)
			if err != nil {
				errors <- err
				return
			}
			defer response.Body.Close()
			got, err := io.ReadAll(io.LimitReader(response.Body, 256))
			if err != nil || response.StatusCode != 200 || response.ProtoMajor != 1 || string(got) != token {
				errors <- fmt.Errorf("short HTTP request failed: %v", err)
			} else {
				if wasUnderBulk && activeBulk.Load() > 0 {
					shortUnderBulk.Add(1)
				}
				completeShortOnce.Do(func() { close(shortCompleted) })
			}
		}(i)
	}
	close(start)
	if capRecovers {
		// HTTP requests are already admitted. Refuse lazy DATA dials while
		// keeping the authenticated carrier sockets alive, then release the
		// external sessions and raise the cap without restarting HTTP or lanes.
		awaitCapacity(t, ctx, func() bool {
			f.network.mu.Lock()
			defer f.network.mu.Unlock()
			return f.network.sessionDenied > deniedBeforeLoad
		}, "provider cap was not exercised by a real DATA login")
		f.network.mu.Lock()
		refusedBefore := f.network.sessionDenied - deniedBeforeLoad
		f.network.sessionLimit = 10
		f.network.mu.Unlock()
		for _, c := range pressure {
			_ = c.Close()
		}
		if refusedBefore == 0 {
			t.Fatal("connection-cap scenario never refused a session")
		}
		awaitCapacity(t, ctx, func() bool {
			for _, old := range snapshots {
				if dataParallelism(old.client.mail) != 1 || dataParallelism(old.server.mail) != 1 {
					return false
				}
			}
			return true
		}, "provider cap cleared but DATA capacity never recovered")
		t.Logf("provider_cap_initial=9 recovered=10 refused_during_http=%d reserved_sessions=%d all_carrier_data_capacity=1", refusedBefore, len(pressure))
	}
	// A fresh short request must be observed while HTTP bulk is actually in
	// flight, not merely started alongside bulk goroutines before negotiation.
	select {
	case <-bulkSeen:
	case <-ctx.Done():
		t.Fatal("HTTP bulk never reached application", ctx.Err())
	}
	close(startShort)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if uploads.Load() != int64(2*len(payload)) || downloads.Load() != int64(2*len(payload)) || short.Load() != 8 {
		t.Fatal("application transfer/request counts", uploads.Load(), downloads.Load(), short.Load(), shortUnderBulk.Load())
	}
	if shortUnderBulk.Load() == 0 {
		t.Fatal("no fresh HTTP request completed during bulk")
	}
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("HTTP workload replaced logical session")
	}
	assertLoadCarriersPreserved(t, f, snapshots)
	peakConnections := 0
	f.network.mu.Lock()
	for account, peak := range f.network.sessionPeak {
		peakConnections = max(peakConnections, peak)
		if peak > 10 {
			t.Errorf("provider connection cap exceeded for %s: %d", account, peak)
		}
	}
	denied := f.network.sessionDenied
	f.network.mu.Unlock()
	t.Logf("application_http2=true upload_bytes=%d download_bytes=%d short_requests=%d short_completed_during_bulk=%d accounts=4 provider_connection_limit=10 peak_connections=%d refused_connections=%d visibility_delay_ms=100 command_delay_ms=%d speed_qualification=false", uploads.Load(), downloads.Load(), short.Load(), shortUnderBulk.Load(), peakConnections, denied, delayMS)
}
