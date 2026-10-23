//go:build itomtest

package tunnel

import (
	"bytes"
	"context"
	"crypto/x509"
	"io"
	"itom.local/core/wire"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"
)

// The earlier TLS fixture performed six RPCs before its first handshake. This
// case starts actual HTTPS as soon as the UI has one working direction pair,
// with no application probe, all-account wait, or warm-up before the request.
func TestDEV9ColdTLSAtFirstReadyAccount(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_DEV7_FIELD_DEBT", "1")
	t.Setenv("ITOM_LOAD_DEV9_FIELD_DEBT", "1")
	t.Setenv("ITOM_LOAD_MANY_RETAINED_GRANTS", "1")
	t.Setenv("ITOM_LOAD_ACCOUNT_SESSIONS", "6")
	t.Setenv("ITOM_LOAD_CLIENT_ACCOUNT_SESSIONS", "4")
	t.Setenv("ITOM_LOAD_ENDPOINT_CONNECTIONS", "45")
	f := newProductionSupervisorFixtureState(t, 15, fieldSequenceHistory(t), func(d Dial) Dial { return d })
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	for f.client.Snapshot().ActiveLines < 2 {
		select {
		case <-timer.C:
			t.Fatal("first ready direction pair missing")
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
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
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		p, _ := strconv.Atoi(port)
		ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		c, _, e := f.client.Open(ctx, wire.Connect{Host: host, Port: uint16(p)})
		if e != nil {
			return nil, e
		}
		return c.(net.Conn), nil
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	fanout := loadEnvInt(t, "ITOM_DEV9_COLD_FANOUT", 1, 1, 16)
	at := time.Now()
	ready := f.client.Snapshot().ActiveLines
	var wg sync.WaitGroup
	for i := 0; i < fanout; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, e := client.Get(srv.URL)
			if e != nil {
				t.Errorf("cold TLS startup worker=%d: %v", i, e)
				return
			}
			defer res.Body.Close()
			got, e := io.ReadAll(res.Body)
			if e != nil || !bytes.Equal(got, payload) {
				t.Errorf("cold HTTPS integrity worker=%d bytes=%d err=%v", i, len(got), e)
			}
		}(i)
	}
	wg.Wait()
	t.Logf("cold_tls variant=%s fanout=%d starting_active_directions=%d elapsed_ms=%d no_application_warmup=true original_deadlines=true", os.Getenv("ITOM_EXPERIMENT_LABEL"), fanout, ready, time.Since(at).Milliseconds())
	if t.Failed() {
		return
	}
	chat := rev4Open(t, f)
	defer chat.Close()
	for i := 0; i < 3; i++ {
		if e := rev4Exchange(chat, i, 32, 750*time.Millisecond); e != nil {
			t.Fatal("post-cold light request", e)
		}
	}
}
