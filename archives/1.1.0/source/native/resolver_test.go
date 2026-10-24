package mobile

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func dnsReply(t *testing.T, raw []byte) []byte {
	t.Helper()
	var q dnsmessage.Message
	if err := q.Unpack(raw); err != nil {
		t.Error(err)
		return nil
	}
	q.Response, q.RecursionAvailable = true, true
	q.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{203, 0, 113, 9}}}}
	reply, err := q.Pack()
	if err != nil {
		t.Error(err)
	}
	return reply
}
func udpResolver(t *testing.T, reply func([]byte) []byte) string {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			buf := make([]byte, 4096)
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if out := reply(buf[:n]); out != nil {
				conn.WriteTo(out, addr)
			}
		}
	}()
	t.Cleanup(func() { conn.Close(); <-done })
	return conn.LocalAddr().String()
}
func forbiddenTunnel(t *testing.T) dnsDial {
	return func(context.Context, string, string) (net.Conn, error) {
		t.Error("direct DNS escaped into the mail transport")
		return nil, errors.New("forbidden")
	}
}
func TestDNSDefaultsFailoverValidationAndCachedAddresses(t *testing.T) {
	s := defaultDNSSettings()
	if s.Primary != "31.131.251.83" || s.Backup != "185.22.234.229" {
		t.Fatal("final user defaults changed", s)
	}
	for _, bad := range []dnsSettings{{Primary: "resolver.example"}, {Primary: s.Primary, Backup: s.Primary}, {Primary: s.Primary, ITOMFallback: true}} {
		if bad.validate() == nil {
			t.Fatal("invalid DNS config accepted")
		}
	}
	var queries atomic.Int32
	address := udpResolver(t, func(raw []byte) []byte { queries.Add(1); return dnsReply(t, raw) })
	var destinations []string
	var destinationMu sync.Mutex
	r, err := newDNSResolver(s, func(ctx context.Context, network, target string) (net.Conn, error) {
		destinationMu.Lock()
		destinations = append(destinations, target)
		destinationMu.Unlock()
		if target == net.JoinHostPort(s.Primary, "53") {
			return nil, errors.New("primary unavailable")
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	query, _ := dnsQuestion("example.test")
	binary.BigEndian.PutUint16(query, 1234)
	reply, err := r.exchange(context.Background(), query)
	if err != nil || !validDNSReply(query, reply) || len(destinations) != 2 {
		t.Fatal("backup/response binding failed", destinations, err)
	}
	ips, err := r.lookup(context.Background(), "example.test")
	if err != nil || len(ips) != 1 || ips[0].String() != "203.0.113.9" || queries.Load() != 1 {
		t.Fatal("application dial repeated or changed DNS lookup", ips, err)
	}
}
func TestTruncatedDirectDNSRetriesTCPAndNeverUsesMail(t *testing.T) {
	udp := udpResolver(t, func(raw []byte) []byte {
		var m dnsmessage.Message
		m.Unpack(raw)
		m.Response, m.Truncated = true, true
		out, _ := m.Pack()
		return out
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := listener.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var h [2]byte
		if _, err = io.ReadFull(c, h[:]); err != nil {
			t.Error(err)
			return
		}
		raw := make([]byte, binary.BigEndian.Uint16(h[:]))
		if _, err = io.ReadFull(c, raw); err != nil {
			t.Error(err)
			return
		}
		reply := dnsReply(t, raw)
		c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(reply))), reply...))
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	var networks []string
	var networkMu sync.Mutex
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		networkMu.Lock()
		networks = append(networks, network)
		networkMu.Unlock()
		address := udp
		if network == "tcp" {
			address = listener.Addr().String()
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	query, _ := dnsQuestion("example.test")
	if _, err := r.exchange(context.Background(), query); err != nil || len(networks) < 2 || !slices.Contains(networks, "udp") || !slices.Contains(networks, "tcp") {
		t.Fatal("truncated response did not use direct TCP", networks, err)
	}
}

type dnsHost struct{ routes []string }

func (h *dnsHost) Protect(int) bool         { return true }
func (*dnsHost) DNS() string                { return "[]" }
func (*dnsHost) Lookup(string) string       { return "[]" }
func (h *dnsHost) Route(name string) string { h.routes = append(h.routes, name); return "DIRECT" }

func TestDirectResolverPreservesDomainRoutingWithSyntheticAddresses(t *testing.T) {
	address := udpResolver(t, func(raw []byte) []byte { return dnsReply(t, raw) })
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDNS(filepath.Join(t.TempDir(), "dns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	host := &dnsHost{}
	c := &Client{ctx: context.Background(), resolver: r, dns: db, host: host}
	query, _ := dnsQuestion("example.test")
	raw, err := c.answer(query)
	if err != nil {
		t.Fatal(err)
	}
	var answer dnsmessage.Message
	if answer.Unpack(raw) != nil || len(answer.Answers) != 1 {
		t.Fatal("synthetic answer missing")
	}
	ip := netip.AddrFrom4(answer.Answers[0].Body.(*dnsmessage.AResource).A)
	name, err := db.name(ip)
	if err != nil || name != "example.test" || !fakePrefix.Contains(ip) {
		t.Fatal("domain association lost", name, err)
	}
	resolved, err := r.lookup(context.Background(), name)
	if err != nil || len(resolved) != 1 || resolved[0].String() != "203.0.113.9" || len(host.routes) != 1 || host.routes[0] != name {
		t.Fatal("resolver or routing identity changed", resolved, err)
	}
}

func TestCanceledDNSDoesNotLeaveAUDPRequestOrFallBackThroughMail(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	address := udpResolver(t, func([]byte) []byte { once.Do(func() { close(started) }); return nil })
	var wg sync.WaitGroup
	r, err := newDNSResolver(defaultDNSSettings(), func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}, forbiddenTunnel(t))
	if err != nil {
		t.Fatal(err)
	}
	query, _ := dnsQuestion("example.test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wg.Go(func() {
		if _, err := r.exchange(ctx, query); err == nil {
			t.Error("canceled DNS query succeeded")
		}
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("DNS request did not start")
	}
	cancel()
	wg.Wait()
	if len(r.cache) != 0 {
		t.Fatal("failed lookup poisoned address cache")
	}
}
