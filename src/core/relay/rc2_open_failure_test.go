package relay

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"itom.local/mailstream"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRC2LegacyExitFailsWithExplicitTLSReason(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	cfg := Config{Address: ln.Addr().String(), Key: strings.Repeat("12", 32), TimeoutMS: 1000}
	p, e := policy(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	p.NextProtos = []string{"olcrtc-relay/2"}
	p.VerifyConnection = nil
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e == nil {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_ = tls.Server(c, p).Handshake()
		}
	}()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if c, e := d.Connect(ctx, "example.com:443"); c != nil || !errors.Is(e, mailstream.ErrExitRelayTLS) {
		t.Fatalf("unexpected legacy result %v %v", c, e)
	}
	<-done
}

func TestRC2TargetDenialPreservesPolicyNoFallback(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	cfg := Config{Address: ln.Addr().String(), Key: strings.Repeat("12", 32), TimeoutMS: 1000}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan error, 1)
	go func() {
		done <- ServeWithDial(ctx, cfg, ln, func(context.Context, string, string) (net.Conn, error) { return nil, mailstream.ErrExitPolicy })
	}()
	defer func() { cancel(); <-done }()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	direct := func(context.Context, string) (net.Conn, error) { t.Fatal("target denial bypassed"); return nil, nil }
	for range 2 {
		c, e := d.Dial(ctx, "example.com:443", direct)
		if c != nil || !errors.Is(e, ErrTarget) || !errors.Is(e, mailstream.ErrExitPolicy) {
			t.Fatal(c, e)
		}
	}
	if !d.retry.IsZero() {
		t.Fatal("target denial poisoned exit cooldown")
	}
}

// Reproduce the RC1 field deployment error with a legacy ALPN listener.
// The failed TLS handshake must not prevent application TCP via the ingress.
func TestRC2ALPNMismatchAutomaticallyUsesDirect(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	cfg := Config{Address: ln.Addr().String(), Key: strings.Repeat("ab", 32), TimeoutMS: 500}
	p, e := policy(cfg, true)
	if e != nil {
		t.Fatal(e)
	}
	p.NextProtos = []string{"olcrtc-relay/2"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := ln.Accept()
		if e == nil {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_ = tls.Server(c, p).Handshake()
		}
	}()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	target := testTarget(t)
	c, e := d.Dial(ctx, target, func(ctx context.Context, dst string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", dst)
	})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.Write([]byte("request")); e != nil {
		t.Fatal(e)
	}
	if e = c.(interface{ CloseWrite() error }).CloseWrite(); e != nil {
		t.Fatal(e)
	}
	var b [64]byte
	if _, e = io.ReadFull(c, b[:]); e != nil {
		t.Fatal(e)
	}
	<-done
	if d.NegotiationCounts()["direct_fallback_established"] != 1 || d.NegotiationCounts()["credit"] != 0 {
		t.Fatal("wrong route counters")
	}
}
