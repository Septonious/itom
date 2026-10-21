package socks

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func connect(t *testing.T, cfg Config, open OpenFunc) (*net.TCPConn, context.CancelFunc, <-chan error) {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, cfg, open) }()
	c, e := net.DialTCP("tcp4", nil, l.Addr().(*net.TCPAddr))
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	t.Cleanup(func() { c.Close(); cancel() })
	return c, cancel, done
}
func readN(t *testing.T, c io.Reader, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, e := io.ReadFull(c, b); e != nil {
		t.Fatal(e)
	}
	return b
}
func req(host string, port uint16) []byte {
	b := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	return binary.BigEndian.AppendUint16(b, port)
}
func TestSOCKSAuthPipelineRemoteDNSAndHalfClose(t *testing.T) {
	destination, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if e != nil {
		t.Fatal(e)
	}
	defer destination.Close()
	destination.SetDeadline(time.Now().Add(3 * time.Second))
	upload := bytes.Repeat([]byte("upload"), 12000)
	download := bytes.Repeat([]byte("reply"), 14000)
	destDone := make(chan error, 1)
	go func() {
		c, e := destination.AcceptTCP()
		if e != nil {
			destDone <- e
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		b, e := io.ReadAll(c)
		if e == nil && !bytes.Equal(b, upload) {
			e = ErrProtocol
		}
		if e == nil {
			e = writeAll(c, download)
		}
		if e == nil {
			e = c.CloseWrite()
		}
		destDone <- e
	}()
	cfg := DefaultConfig()
	cfg.Auth, e = NewCredentials("user", "pass")
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	c, cancel, done := connect(t, cfg, func(ctx context.Context, target wire.Connect) (Stream, netip.AddrPort, error) {
		calls.Add(1)
		if target.Host != "example.invalid" || target.Port != 443 {
			return nil, netip.AddrPort{}, ErrProtocol
		}
		// This fake transport maps the unresolved name to our test destination.
		raw, e := (&net.Dialer{}).DialContext(ctx, "tcp4", destination.Addr().String())
		if e != nil {
			return nil, netip.AddrPort{}, e
		}
		tcp := raw.(*net.TCPConn)
		return tcp, tcp.LocalAddr().(*net.TCPAddr).AddrPort(), nil
	})
	// Pipeline handshake, auth, CONNECT and application bytes in one write:
	// the parser must not buffer/discard any application bytes.
	b := []byte{5, 2, 0, 2, 1, 4, 'u', 's', 'e', 'r', 4, 'p', 'a', 's', 's'}
	b = append(b, req("Example.Invalid.", 443)...)
	b = append(b, upload...)
	if e = writeAll(c, b); e != nil {
		t.Fatal(e)
	}
	if got := readN(t, c, 2); !bytes.Equal(got, []byte{5, 2}) {
		t.Fatal(got)
	}
	if got := readN(t, c, 2); !bytes.Equal(got, []byte{1, 0}) {
		t.Fatal(got)
	}
	if got := readN(t, c, 10); got[1] != 0 {
		t.Fatal(got)
	}
	if e = c.CloseWrite(); e != nil {
		t.Fatal(e)
	}
	got, e := io.ReadAll(c)
	if e != nil || !bytes.Equal(got, download) {
		t.Fatal("response after upload EOF", len(got), e)
	}
	if e = <-destDone; e != nil {
		t.Fatal(e)
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate open")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handlers not joined")
	}
}
func TestAuthenticationCannotFallBackOrOpenOnBadPassword(t *testing.T) {
	for _, bad := range []string{"missing-method", "wrong-password", "zero-username"} {
		t.Run(bad, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth, _ = NewCredentials("user", "pass")
			var calls atomic.Int32
			c, cancel, done := connect(t, cfg, func(context.Context, wire.Connect) (Stream, netip.AddrPort, error) {
				calls.Add(1)
				return nil, netip.AddrPort{}, ErrProtocol
			})
			if bad == "missing-method" {
				_ = writeAll(c, []byte{5, 1, 0})
				if got := readN(t, c, 2); !bytes.Equal(got, []byte{5, 255}) {
					t.Fatal(got)
				}
			} else {
				_ = writeAll(c, []byte{5, 1, 2})
				_ = readN(t, c, 2)
				b := []byte{1, 4, 'u', 's', 'e', 'r', 4, 'b', 'a', 'd', '!'}
				if bad == "zero-username" {
					b = []byte{1, 0}
				}
				_ = writeAll(c, b)
				if got := readN(t, c, 2); !bytes.Equal(got, []byte{1, 1}) {
					t.Fatal(got)
				}
			}
			if calls.Load() != 0 {
				t.Fatal("opened before auth")
			}
			cancel()
			<-done
		})
	}
}
func TestUnsupportedRequestsNeverOpen(t *testing.T) {
	for _, b := range [][]byte{{5, 3, 0, 1}, {5, 2, 0, 1}, {5, 1, 0, 4}, {5, 1, 1, 1}} {
		t.Run(string([]byte{'0' + b[1], '0' + b[2], '0' + b[3]}), func(t *testing.T) {
			c, cancel, done := connect(t, DefaultConfig(), func(context.Context, wire.Connect) (Stream, netip.AddrPort, error) {
				t.Error("opened unsupported request")
				return nil, netip.AddrPort{}, ErrProtocol
			})
			_ = writeAll(c, []byte{5, 1, 0})
			_ = readN(t, c, 2)
			_ = writeAll(c, b)
			if got := readN(t, c, 10); got[1] == 0 {
				t.Fatal(got)
			}
			cancel()
			<-done
		})
	}
}
func TestCancellationUnblocksHandshakeAndRejectsWildcard(t *testing.T) {
	c, cancel, done := connect(t, DefaultConfig(), func(context.Context, wire.Connect) (Stream, netip.AddrPort, error) {
		return nil, netip.AddrPort{}, ErrProtocol
	})
	_ = writeAll(c, []byte{5})
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stuck incomplete handshake")
	}
	l, e := net.Listen("tcp4", "0.0.0.0:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	if e = Serve(context.Background(), l, DefaultConfig(), func(context.Context, wire.Connect) (Stream, netip.AddrPort, error) {
		return nil, netip.AddrPort{}, ErrProtocol
	}); e != ErrConfig {
		t.Fatal(e)
	}
}

func TestConfiguredPortConflictNeverChoosesAnotherPort(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port
	if e = ListenAndServe(context.Background(), Endpoint{Port: port}, func(context.Context, wire.Connect) (Stream, netip.AddrPort, error) {
		t.Error("unexpected transport open")
		return nil, netip.AddrPort{}, ErrProtocol
	}); e == nil {
		t.Fatal("bind conflict hidden")
	}
	if e = ListenAndServe(context.Background(), Endpoint{Port: port, Authentication: true}, func(context.Context, wire.Connect) (Stream, netip.AddrPort, error) {
		return nil, netip.AddrPort{}, ErrProtocol
	}); e != ErrConfig {
		t.Fatal("empty credentials accepted", e)
	}
}
