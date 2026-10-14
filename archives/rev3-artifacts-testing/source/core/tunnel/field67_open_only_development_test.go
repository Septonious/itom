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
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestField67TenAccountSpeedtestOpeningRecoveryDevelopment(t *testing.T) {
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "15")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	f := newProductionSupervisorFixtureAccounts(t, 10)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "itom-burst-ok")
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true, MaxConnsPerHost: 24,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
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
				return nil, fmt.Errorf("unexpected conn %T", c)
			}
			return conn, nil
		}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	errors := make(chan error, 128)
	var wg sync.WaitGroup
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				response, err := client.Get(server.URL)
				if err == nil {
					body, e := io.ReadAll(response.Body)
					response.Body.Close()
					err = e
					if string(body) != "itom-burst-ok" {
						err = fmt.Errorf("corrupt HTTPS body")
					}
				}
				if err != nil {
					errors <- err
					return
				}
				// Close a successfully OPENed socket without exchanging application bytes.
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				c, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(f.listener.Addr().(*net.TCPAddr).Port)})
				if err == nil {
					err = c.Close()
				}
				cancel()
				if err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	// Exercise idle/restart after short-connection burst without resetting any
	// durable state or carrier. The full five-minute idle remains a release gate.
	time.Sleep(2 * time.Second)
	// Allow terminal ACKs and the next liveness probe to drain; then prove a new
	// application request still works and none of the original carriers exited.
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal("HTTPS after burst", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "itom-burst-ok" {
		t.Fatal(err)
	}
	if n := f.carrierExits.unexpected.Load(); n != 0 {
		t.Fatalf("short-connection load disconnected %d carriers: %v", n, f.carrierExits.failureDetails())
	}
	for i := 1; i <= 10; i++ {
		l := f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
		if l.carrierEpoch != 1 {
			t.Fatal("carrier silently rebuilt", l.account.ID, l.carrierEpoch)
		}
	}
	t.Log("verified_https=73 canceled_open_sockets=72 provider_accounts=10 carrier_rebuilds=0; functional workload, not a bandwidth measurement")
}

func TestField67LegacyJournalSpeedtestOpeningRecoveryDevelopment(t *testing.T) {
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "15")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	f := newProductionSupervisorFixtureState(t, 10, fieldLegacyState(t))
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "itom-burst-ok")
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DisableKeepAlives: true, MaxConnsPerHost: 24,
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
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
				return nil, fmt.Errorf("unexpected conn %T", c)
			}
			return conn, nil
		}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	errors := make(chan error, 128)
	var wg sync.WaitGroup
	for worker := 0; worker < 24; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				response, err := client.Get(server.URL)
				if err == nil {
					body, e := io.ReadAll(response.Body)
					response.Body.Close()
					err = e
					if string(body) != "itom-burst-ok" {
						err = fmt.Errorf("corrupt HTTPS body")
					}
				}
				if err != nil {
					errors <- err
					return
				}
				// Close a successfully OPENed socket without exchanging application bytes.
				ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				c, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(f.listener.Addr().(*net.TCPAddr).Port)})
				if err == nil {
					err = c.Close()
				}
				cancel()
				if err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	// Exercise idle/restart after short-connection burst without resetting any
	// durable state or carrier. The full five-minute idle remains a release gate.
	time.Sleep(2 * time.Second)
	// Allow terminal ACKs and the next liveness probe to drain; then prove a new
	// application request still works and none of the original carriers exited.
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal("HTTPS after burst", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "itom-burst-ok" {
		t.Fatal(err)
	}
	if n := f.carrierExits.unexpected.Load(); n != 0 {
		t.Fatalf("short-connection load disconnected %d carriers: %v", n, f.carrierExits.failureDetails())
	}
	for i := 1; i <= 10; i++ {
		l := f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
		if l.carrierEpoch != 1 {
			t.Fatal("carrier silently rebuilt", l.account.ID, l.carrierEpoch)
		}
	}
	t.Log("verified_https=73 canceled_open_sockets=72 provider_accounts=10 carrier_rebuilds=0; functional workload, not a bandwidth measurement")
}
