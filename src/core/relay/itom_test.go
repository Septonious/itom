package relay

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestRC5RelayDefaultFailClosed(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	addr := ln.Addr().String()
	ln.Close()
	d, e := New(Config{Address: addr, Key: strings.Repeat("ab", 32), TimeoutMS: 100})
	if e != nil {
		t.Fatal(e)
	}
	direct := func(context.Context, string) (net.Conn, error) {
		t.Fatal("silent direct fallback")
		return nil, errors.New("unexpected")
	}
	for range 2 {
		if conn, e := d.Dial(context.Background(), "example.com:443", direct); e == nil {
			conn.Close()
			t.Fatal("unavailable exit succeeded")
		}
	}
}
func TestRC5RelayPublicPolicy(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := Config{Address: ln.Addr().String(), Key: strings.Repeat("12", 32), TimeoutMS: 1000}
	go func() { done <- Serve(ctx, cfg, ln) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("exit did not stop")
		}
	}()
	target := testTarget(t)
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if c, e := d.Connect(ctx, target); !errors.Is(e, ErrTarget) {
		if c != nil {
			c.Close()
		}
		t.Fatalf("private target not denied: %v", e)
	}
}

func TestRC5MeasuredRelayPayload(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cfg := Config{Address: ln.Addr().String(), Key: strings.Repeat("12", 32), TimeoutMS: 1000}
	m := &Metrics{}
	go func() { done <- serve(ctx, cfg, ln, (&net.Dialer{}).DialContext, m) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("exit did not stop")
		}
	}()
	d, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	c, e := d.Connect(ctx, testTarget(t))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	p := []byte(strings.Repeat("field-payload", 8192))
	if _, e = c.Write(p); e != nil {
		t.Fatal(e)
	}
	if e = c.(interface{ CloseWrite() error }).CloseWrite(); e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(c)
	if e != nil || len(b) != 64 {
		t.Fatalf("response %d %v", len(b), e)
	}
	s := m.Snapshot()
	if s.UploadBytes != uint64(len(p)) || s.DownloadBytes != 64 {
		t.Fatalf("incorrect foreign counters %+v", s)
	}
}
