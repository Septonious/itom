package netroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestPlatformDNSProtectedDualStackAndCancellation(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var protected atomic.Int32
	blockedDone := make(chan struct{})
	d := &Dialer{Socket: net.Dialer{Timeout: time.Second, FallbackDelay: 20 * time.Millisecond,
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			protected.Add(1)
			if network == "tcp6" {
				defer close(blockedDone)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}}, Lookup: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("::1"), netip.MustParseAddr("127.0.0.1")}, nil
	}}
	address := net.JoinHostPort("provider.example", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	started := time.Now()
	c, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if protected.Load() != 2 || time.Since(started) > 500*time.Millisecond {
		t.Fatal("fallback was delayed or unprotected")
	}
	select {
	case <-blockedDone:
	case <-ctx.Done():
		t.Fatal("losing family did not cancel")
	}
	// An IP literal bypasses lookup. tcp4 excludes IPv6 before socket creation.
	d.Lookup = func(context.Context, string) ([]netip.Addr, error) { t.Fatal("literal consulted DNS"); return nil, nil }
	c2, err := d.DialContext(ctx, "tcp4", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c2.Close()
	d.Lookup = func(ctx context.Context, _ string) ([]netip.Addr, error) { <-ctx.Done(); return nil, ctx.Err() }
	d.Socket.Timeout = 30 * time.Millisecond
	_, err = d.DialContext(ctx, "tcp", address)
	var dns *net.DNSError
	if !errors.As(err, &dns) || !dns.IsTimeout {
		t.Fatal("DNS timeout lost its diagnostic stage", err)
	}
}
