package mobile

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tcpResolver(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Go(func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				var header [2]byte
				if _, err := io.ReadFull(conn, header[:]); err != nil {
					return
				}
				n := binary.BigEndian.Uint16(header[:])
				if n > maxDNSBytes {
					t.Error("oversize TCP DNS query")
					return
				}
				query := make([]byte, n)
				if _, err := io.ReadFull(conn, query); err != nil {
					return
				}
				reply := dnsReply(t, query)
				conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(reply))), reply...))
			})
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done; workers.Wait() })
	return listener.Addr().String()
}

func TestBothDirectResolversRaceAndFirstValidAnswerNeverUsesIMAP(t *testing.T) {
	for _, winner := range []string{primaryDNS, backupDNS} {
		t.Run(winner, func(t *testing.T) {
			var queries atomic.Int32
			ready := make(chan struct{})
			var once sync.Once
			fast := udpResolver(t, func(query []byte) []byte {
				if queries.Add(1) == 2 {
					once.Do(func() { close(ready) })
				}
				select {
				case <-ready:
				case <-time.After(time.Second):
					t.Error("resolvers were not concurrent")
					return nil
				}
				return dnsReply(t, query)
			})
			slow := udpResolver(t, func([]byte) []byte {
				if queries.Add(1) == 2 {
					once.Do(func() { close(ready) })
				}
				return nil
			})
			settings := defaultDNSSettings()
			settings.ITOMFallback = true
			r, err := newDNSResolver(settings, func(ctx context.Context, network, address string) (net.Conn, error) {
				host, _, _ := net.SplitHostPort(address)
				actual := slow
				if host == winner {
					actual = fast
				}
				return (&net.Dialer{}).DialContext(ctx, network, actual)
			}, forbiddenTunnel(t))
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				query, _ := dnsQuestion("example.test")
				if _, err := r.exchange(context.Background(), query); err != nil {
					t.Fatal("usable direct resolver lost race", err)
				}
			}
			if r.imap.Load() {
				t.Fatal("a healthy direct answer latched IMAP")
			}
		})
	}
}

func TestBoth250msTimeoutsLatchIMAPUntilANewVPNSession(t *testing.T) {
	if directDNSTimeout != 250*time.Millisecond {
		t.Fatal("user DNS timeout changed")
	}
	dropped := udpResolver(t, func([]byte) []byte { return nil })
	imap := tcpResolver(t)
	var directCalls, imapCalls atomic.Int32
	settings := defaultDNSSettings()
	settings.ITOMFallback = true
	direct := func(ctx context.Context, network, _ string) (net.Conn, error) {
		directCalls.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 250*time.Millisecond {
			t.Error("direct attempt has no 250ms deadline")
		}
		return (&net.Dialer{}).DialContext(ctx, network, dropped)
	}
	tunnel := func(ctx context.Context, network, _ string) (net.Conn, error) {
		if directCalls.Load() != 2 || network != "tcp" {
			t.Error("IMAP started before both direct attempts")
		}
		imapCalls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, imap)
	}
	r, err := newDNSResolver(settings, direct, tunnel)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		query, _ := dnsQuestion("example.test")
		if _, err := r.exchange(context.Background(), query); err != nil {
			t.Fatal("session fallback failed", err)
		}
	}
	if directCalls.Load() != 2 || imapCalls.Load() != 3 {
		t.Fatal("latched session probed direct DNS again", directCalls.Load(), imapCalls.Load())
	}
	// New native clients are created only at a new VPN generation. Reconnecting
	// IMAP lines/network handovers do not replace this resolver or reset its latch.
	fresh, err := newDNSResolver(settings, func(context.Context, string, string) (net.Conn, error) {
		directCalls.Add(1)
		return nil, errors.New("fresh attempt")
	}, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("no mail") })
	if err != nil {
		t.Fatal(err)
	}
	query, _ := dnsQuestion("example.test")
	fresh.exchange(context.Background(), query)
	if directCalls.Load() != 4 {
		t.Fatal("new VPN generation did not retry both direct resolvers")
	}
}

func TestDisabledFallbackOrCallerCancellationCannotLatchIMAP(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := defaultDNSSettings()
		s.ITOMFallback = enabled
		r, err := newDNSResolver(s, func(ctx context.Context, _, _ string) (net.Conn, error) { return nil, ctx.Err() }, forbiddenTunnel(t))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		query, _ := dnsQuestion("example.test")
		if _, err := r.exchange(ctx, query); err == nil || r.imap.Load() {
			t.Fatal("canceled call enabled session fallback", err)
		}
	}
}

func TestDisabledFallbackKeepsFailuresOffMailAndRetriesDirectNextQuery(t *testing.T) {
	var calls atomic.Int32
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, _, _ string) (net.Conn, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		query, _ := dnsQuestion("example.test")
		if _, err := r.exchange(context.Background(), query); err == nil {
			t.Fatal("blackholed resolvers succeeded")
		}
	}
	if calls.Load() != 4 || r.imap.Load() {
		t.Fatal("disabled fallback changed session DNS routing", calls.Load())
	}
}
