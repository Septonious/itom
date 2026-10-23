//go:build itomtest

package tunnel

import (
	"bytes"
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Short TLS/HTTP setup, aborted ramps, and a reused light-load connection are
// distinct from the long, already-established bulk streams in the rate gates.
// Both endpoints retain the normal durable journals; the server path remains
// configurable (20 ms by default). No provider fault or zero-spend proof exists.
func TestDEV8RepeatedTLSStartsAndAbortsWithRetainedGrants(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_DEV7_FIELD_DEBT", "1")
	t.Setenv("ITOM_LOAD_MANY_RETAINED_GRANTS", "1")
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "6")
	t.Setenv("ITOM_LOAD_CLIENT_ACCOUNT_SESSIONS", "4")
	t.Setenv("ITOM_LOAD_ENDPOINT_CONNECTIONS", "45")
	f := newProductionSupervisorFixtureState(t, 15, fieldSequenceHistory(t))
	logRoles := func(side, id string, m any) {
		if sockets, ok := m.(interface {
			SocketSnapshots() []mailbox.SocketSnapshot
		}); ok {
			t.Logf("dev8_endpoint_roles side=%s account=%s sockets=%+v", side, id, sockets.SocketSnapshots())
		}
	}
	f.client.mu.Lock()
	for id, l := range f.client.lanes {
		logRoles("client", id, physicalMail(l.mail))
	}
	f.client.mu.Unlock()
	f.mu.Lock()
	for id, l := range f.serverLanes {
		logRoles("server", id, physicalMail(l.mail))
	}
	f.mu.Unlock()
	payload := make([]byte, 512<<10)
	for i := range payload {
		payload[i] = byte((i*131 + 17) % 251)
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.RootCAs = roots
	tr.DisableKeepAlives = true
	tr.ForceAttemptHTTP2 = false
	tr.TLSHandshakeTimeout = 1500 * time.Millisecond
	tr.ResponseHeaderTimeout = 1500 * time.Millisecond
	tr.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		p, _ := strconv.Atoi(port)
		ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		c, _, err := f.client.Open(ctx, wire.Connect{Host: host, Port: uint16(p)})
		if err != nil {
			return nil, err
		}
		return c.(net.Conn), nil
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	chat := rev4Open(t, f)
	defer chat.Close()
	for round, fanout := range []int{1, 4, 4, 1} {
		for phase := 0; phase < 2; phase++ {
			if phase == 1 {
				var wg sync.WaitGroup
				for i := 0; i < fanout; i++ {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						res, err := client.Get(srv.URL)
						if err != nil {
							t.Errorf("TLS startup round=%d worker=%d: %v", round, i, err)
							return
						}
						defer res.Body.Close()
						size := len(payload)
						if i%2 == 0 {
							size = 128 << 10
						}
						got, err := io.ReadAll(io.LimitReader(res.Body, int64(size)))
						if err != nil || !bytes.Equal(got, payload[:size]) {
							t.Errorf("ramp integrity round=%d worker=%d bytes=%d: %v", round, i, len(got), err)
						}
					}(i)
				}
				wg.Wait()
			}
			for probe := 0; probe < 6; probe++ {
				time.Sleep(150 * time.Millisecond)
				started := time.Now()
				if err := rev4Exchange(chat, round*100+phase*10+probe, 32, 750*time.Millisecond); err != nil {
					// A timed-out response may still arrive. Stop this stream;
					// comparing it to the next request would invent corruption.
					t.Fatalf("light request round=%d phase=%d probe=%d: %v", round, phase, probe, err)
				}
				t.Logf("dev8_light round=%d phase=%d probe=%d rtt_us=%d", round, phase, probe, time.Since(started).Microseconds())
			}
		}
	}
}
