package transport_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuthenticatedOpenCarriesColdTLSHTTPWithoutManualStreams(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "verified application bytes") }))
	defer upstream.Close()
	address := netip.MustParseAddrPort(upstream.Listener.Addr().String())
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	ca, cb := cipherPair(t, wire.ID{91})
	dialer, err := transport.NewClientDialer(a, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	var authorized atomic.Int32
	exit, err := transport.NewExitServer(b, cb, 256<<10, 4, transport.ExitPolicyFunc(func(ctx context.Context, user string, target transport.Target) (netip.AddrPort, error) {
		if user != "12345678" || target.Host != address.Addr().String() || target.Port != address.Port() {
			return netip.AddrPort{}, transport.OpenDenied
		}
		authorized.Add(1)
		return address, nil
	}), &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(linkSessions(t, a, b, ca, cb))
	done := make(chan error, 1)
	go func() { done <- exit.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	h := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond, DisableKeepAlives: true}
	defer h.CloseIdleConnections()
	client := &http.Client{Transport: h, Timeout: 5 * time.Second}
	for range 20 {
		response, err := client.Get(upstream.URL)
		if err != nil {
			t.Fatal("authenticated OPEN/TLS/HTTP failed", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "verified application bytes" {
			t.Fatal("application body changed", err)
		}
	}
	if authorized.Load() != 20 {
		t.Fatal("OPEN duplicated or bypassed authorization", authorized.Load())
	}
}

func TestResetBeforeOpenPreventsLateTargetConnection(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	_, cb := cipherPair(t, wire.ID{92})
	var dials atomic.Int32
	_, err := transport.NewExitServer(s, cb, 256<<10, 1, transport.ExitPolicyFunc(func(context.Context, string, transport.Target) (netip.AddrPort, error) {
		dials.Add(1)
		return netip.AddrPort{}, transport.OpenDenied
	}), &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	reset := incoming(transport.Reset, 1, 0, nil)
	if err := s.Receive(reset); err != nil {
		t.Fatal(err)
	}
	ack := take(t, s, transport.Fast, 0)
	if ack.Record.Kind != transport.ResetAck {
		t.Fatal("early reset was not acknowledged")
	}
	complete(t, ack, transport.Accepted)
	late := incoming(transport.Open, 1, 0, []byte{1, 187, 11, 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm'})
	late.ReceiveLimit = 256 << 10
	if err := s.Receive(late); !errors.Is(err, transport.ErrClosed) {
		t.Fatal("late OPEN recreated canceled stream", err)
	}
	if s.Snapshot().Streams != 0 || dials.Load() != 0 {
		t.Fatal("early cancellation leaked resources or dialed")
	}
}

func TestOpenAuthorizationCancellationIsIndependentOfOtherStreams(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			io.Copy(conn, conn)
		}
	}()
	address := netip.MustParseAddrPort(listener.Addr().String())
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	ca, cb := cipherPair(t, wire.ID{93})
	dialer, err := transport.NewClientDialer(a, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	started, stopped := make(chan struct{}), make(chan struct{})
	exit, err := transport.NewExitServer(b, cb, 256<<10, 2, transport.ExitPolicyFunc(func(ctx context.Context, user string, target transport.Target) (netip.AddrPort, error) {
		if target.Host == "slow.example" {
			close(started)
			<-ctx.Done()
			close(stopped)
			return netip.AddrPort{}, ctx.Err()
		}
		return address, nil
	}), &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(linkSessions(t, a, b, ca, cb))
	done := make(chan error, 1)
	go func() { done <- exit.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	slow, stop := context.WithCancel(ctx)
	failed := make(chan error, 1)
	go func() {
		c, err := dialer.DialContext(slow, "tcp", net.JoinHostPort("slow.example", fmtPort(address.Port())))
		if c != nil {
			c.Close()
		}
		failed <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow authorization never started")
	}
	conn, err := dialer.DialContext(ctx, "tcp", address.String())
	if err != nil {
		t.Fatal("slow request blocked unrelated OPEN", err)
	}
	defer conn.Close()
	stop()
	if err := <-failed; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("canceled OPEN did not cancel policy worker")
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte("still alive")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 11)
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "still alive" {
		t.Fatal("cancel affected another connection", err)
	}
}

func fmtPort(port uint16) string { return strconv.Itoa(int(port)) }
