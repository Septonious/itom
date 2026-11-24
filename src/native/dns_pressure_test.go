package mobile

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type pressureDNSHost struct{ block bool }

func (pressureDNSHost) Protect(int) bool     { return true }
func (pressureDNSHost) DNS() string          { return "[]" }
func (pressureDNSHost) Lookup(string) string { return "[]" }
func (pressureDNSHost) Startup(string) bool  { return true }
func (h pressureDNSHost) Route(string) string {
	if h.block {
		return "BLOCK"
	}
	return "VPN"
}

func TestCachedDNSBurstPreservesTTLIDsAndAvoidsNetworkAndStorageWrites(t *testing.T) {
	var direct, tunnel atomic.Int32
	r, err := newDNSResolver(defaultDNSSettings(), func(context.Context, string, string) (net.Conn, error) {
		direct.Add(1)
		return nil, errors.New("direct unavailable")
	}, func(context.Context, string, string) (net.Conn, error) {
		tunnel.Add(1)
		return nil, errors.New("tunnel unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDNS(filepath.Join(t.TempDir(), "dns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const name = "cached.test"
	ip, err := db.address(name)
	if err != nil {
		t.Fatal(err)
	}
	r.cache[name] = dnsCacheEntry{ips: []netip.Addr{netip.MustParseAddr("203.0.113.9")}, until: time.Now().Add(5 * time.Second)}
	c := &Client{ctx: context.Background(), resolver: r, dns: db, host: pressureDNSHost{}}
	before := db.db.Stats()
	for i := 0; i < 100; i++ {
		query, _ := dnsQuestion(name)
		binary.BigEndian.PutUint16(query, uint16(i+1))
		reply, err := c.answer(query)
		var answer dnsmessage.Message
		if err != nil || !validDNSReply(query, reply) || answer.Unpack(reply) != nil || answer.RCode != dnsmessage.RCodeSuccess || len(answer.Answers) != 1 {
			t.Fatalf("cached A answer repeated a network query or lost transaction binding: err=%v", err)
		}
		if got := answer.Answers[0]; got.Header.TTL > 5 || got.Body.(*dnsmessage.AResource).A != ip.As4() {
			t.Fatal("cached answer changed its remaining TTL or fake address")
		}
	}
	after := db.db.Stats()
	if direct.Load() != 0 || tunnel.Load() != 0 || after.TxStats.GetWrite() != before.TxStats.GetWrite() {
		t.Fatal("cached DNS burst performed network or storage writes", direct.Load(), tunnel.Load())
	}
	c.host = pressureDNSHost{block: true}
	query, _ := dnsQuestion(name)
	reply, err := c.answer(query)
	var blocked dnsmessage.Message
	if err != nil || blocked.Unpack(reply) != nil || blocked.RCode != dnsmessage.RCodeNameError {
		t.Fatal("cache bypassed domain blocking")
	}
	c.host = pressureDNSHost{}
	r.imap.Store(true)
	if _, err = c.answer(query); err != nil || !r.imap.Load() || direct.Load() != 0 || tunnel.Load() != 0 {
		t.Fatal("cache reset the VPN-scoped fallback latch")
	}
	r.imap.Store(false)
	r.cache[name] = dnsCacheEntry{ips: r.cache[name].ips, until: time.Now().Add(-time.Second)}
	reply, err = c.answer(query)
	var expired dnsmessage.Message
	if err != nil || expired.Unpack(reply) != nil || expired.RCode != dnsmessage.RCodeServerFailure || direct.Load() != 2 || tunnel.Load() != 0 {
		t.Fatal("expired DNS cache bypassed the existing two-resolver policy")
	}
}

func TestAnsweredDNSBurstReleasesWorkerBeforeNextBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, host: pressureDNSHost{}, slots: make(chan struct{}, 128), dnsSlots: make(chan struct{}, 64)}
	defer func() { cancel(); c.wg.Wait() }()
	a, b := net.Pipe()
	defer b.Close()
	c.handleUDP(dnsAssociation{a})
	for i := 0; i < 2; i++ {
		query := dnsmessage.Message{Header: dnsmessage.Header{ID: uint16(i + 1)}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("local.test."), Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET}}}
		raw, _ := query.Pack()
		b.SetDeadline(time.Now().Add(time.Second))
		if _, err := b.Write(raw); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, maxDNSBytes)
		n, err := b.Read(buffer)
		if err != nil || !validDNSReply(raw, buffer[:n]) {
			t.Fatal("pending DNS question was dropped", err)
		}
	}
	b.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := b.Read(make([]byte, maxDNSBytes)); !errors.Is(err, io.EOF) {
		t.Fatal("answered DNS worker retained an idle association across the next request burst", err)
	}
}
