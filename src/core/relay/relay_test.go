package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ai-generated: test an authenticated listener with deterministic cleanup.
func testExit(t *testing.T) (*Dialer, context.CancelFunc) {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	c := Config{Address: ln.Addr().String(), Key: strings.Repeat("12", 32), TimeoutMS: 1000}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeWithDial(ctx, c, ln, (&net.Dialer{}).DialContext) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("exit did not stop")
		}
	})
	d, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	return d, cancel
}

// ai-generated: respond only after target receives a TCP half-close.
func testTarget(t *testing.T) string {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(3 * time.Second))
				b, _ := io.ReadAll(c)
				h := sha256.Sum256(b)
				_, _ = c.Write([]byte(hex.EncodeToString(h[:])))
			}()
		}
	}()
	return ln.Addr().String()
}

// ai-generated: verify encrypted binary transfer and bidirectional half-close handling.
func TestRelayPayloadAndHalfClose(t *testing.T) {
	d, _ := testExit(t)
	c, e := d.Connect(context.Background(), testTarget(t))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	b := bytes.Repeat([]byte{0, 255, 1, 13, 10}, 65536)
	if _, e = c.Write(b); e != nil {
		t.Fatal(e)
	}
	_ = c.(interface{ CloseWrite() error }).CloseWrite()
	got, e := io.ReadAll(c)
	if e != nil {
		t.Fatal(e)
	}
	h := sha256.Sum256(b)
	if string(got) != hex.EncodeToString(h[:]) {
		t.Fatal("payload or half-close changed")
	}
}

// ai-generated: mismatched PSKs cannot issue an authenticated CONNECT.
func TestWrongKeyRejected(t *testing.T) {
	d, _ := testExit(t)
	c := d.cfg
	c.Key = strings.Repeat("34", 32)
	bad, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	if conn, e := bad.Connect(context.Background(), testTarget(t)); e == nil {
		conn.Close()
		t.Fatal("wrong key accepted")
	}
}

func TestAutomaticDirectFallback(t *testing.T) {
	d, e := New(Config{Address: "127.0.0.1:1", Key: strings.Repeat("ab", 32), TimeoutMS: 100})
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	direct := func(context.Context, string) (net.Conn, error) { calls++; a, b := net.Pipe(); b.Close(); return a, nil }
	for range 2 {
		c, e := d.Dial(context.Background(), "example.com:443", direct)
		if e != nil {
			t.Fatal(e)
		}
		c.Close()
	}
	if calls != 2 || d.retry.IsZero() || d.NegotiationCounts()["direct_fallback_established"] != 2 {
		t.Fatal("fallback/cooldown missing")
	}
}

// ai-generated: independent TCP connections operate concurrently.
func TestConcurrentRelay(t *testing.T) {
	d, _ := testExit(t)
	target := testTarget(t)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := d.Connect(context.Background(), target)
			if e != nil {
				t.Error(e)
				return
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = c.Write([]byte("payload"))
			_ = c.(interface{ CloseWrite() error }).CloseWrite()
			b, e := io.ReadAll(c)
			if e != nil || len(b) != 64 {
				t.Error("bad relay response", e)
			}
		}()
	}
	wg.Wait()
}

// ai-generated: process shutdown closes established streams too.
func TestCancellation(t *testing.T) {
	d, cancel := testExit(t)
	c, e := d.Connect(context.Background(), testTarget(t))
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	cancel()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, e = c.Read(b[:]); e == nil {
		t.Fatal("connection survived shutdown")
	}
}

// ai-generated: shutdown must also close a destination that never responds or reads EOF.
func TestShutdownWithSilentTarget(t *testing.T) {
	d, cancel := testExit(t)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	c, e := d.Connect(context.Background(), ln.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	cancel()
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, e = c.Read(b[:]); e == nil {
		t.Fatal("silent target connection survived shutdown")
	}
}
