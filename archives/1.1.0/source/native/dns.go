package mobile

import (
	"encoding/binary"
	"errors"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"strings"
	"time"
)

var fakePrefix = netip.MustParsePrefix("198.18.0.0/15")

type fakeDNS struct{ db *bolt.DB }

func openDNS(path string) (*fakeDNS, error) {
	db, e := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if e != nil {
		return nil, e
	}
	e = db.Update(func(t *bolt.Tx) error {
		_, e := t.CreateBucketIfNotExists([]byte("names"))
		if e != nil {
			return e
		}
		_, e = t.CreateBucketIfNotExists([]byte("ips"))
		return e
	})
	if e != nil {
		db.Close()
		return nil, e
	}
	return &fakeDNS{db}, nil
}
func (d *fakeDNS) Close() { d.db.Close() }
func (d *fakeDNS) address(name string) (netip.Addr, error) {
	var raw [4]byte
	e := d.db.Update(func(t *bolt.Tx) error {
		names := t.Bucket([]byte("names"))
		ips := t.Bucket([]byte("ips"))
		if b := names.Get([]byte(name)); len(b) == 4 {
			copy(raw[:], b)
			return nil
		}
		n, e := names.NextSequence()
		if e != nil {
			return e
		}
		if n >= 131071 {
			return errors.New("DNS_TABLE_FULL")
		}
		binary.BigEndian.PutUint32(raw[:], uint32(0xc6120000+n))
		if e = names.Put([]byte(name), raw[:]); e != nil {
			return e
		}
		return ips.Put(raw[:], []byte(name))
	})
	return netip.AddrFrom4(raw), e
}
func (d *fakeDNS) name(ip netip.Addr) (string, error) {
	var name string
	b := ip.As4()
	e := d.db.View(func(t *bolt.Tx) error {
		raw := t.Bucket([]byte("ips")).Get(b[:])
		if len(raw) == 0 {
			return errors.New("UNKNOWN_FAKE_IP")
		}
		name = string(raw)
		return nil
	})
	return name, e
}
func (c *Client) answer(raw []byte) ([]byte, error) {
	var request dnsmessage.Message
	if len(raw) > 4096 || request.Unpack(raw) != nil || request.Header.Response || len(request.Questions) != 1 {
		return nil, errors.New("DNS_QUERY")
	}
	q := request.Questions[0]
	name := strings.ToLower(strings.TrimSuffix(q.Name.String(), "."))
	if !wire.ValidHost(name) {
		return nil, errors.New("DNS_NAME")
	}
	response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, RecursionDesired: request.RecursionDesired, RecursionAvailable: true}, Questions: request.Questions}
	if c.host.Route(name) == "BLOCK" {
		response.RCode = dnsmessage.RCodeNameError
		return response.Pack()
	}
	if q.Type == dnsmessage.TypeAAAA {
		return response.Pack()
	}
	if c.resolver == nil {
		return nil, errors.New("DNS_CONFIG")
	}
	reply, err := c.resolver.exchange(c.ctx, raw)
	if err != nil {
		c.traffic.dnsFailures.Add(1)
		response.RCode = dnsmessage.RCodeServerFailure
		return response.Pack()
	}
	if q.Type == dnsmessage.TypeA && q.Class == dnsmessage.ClassINET {
		addresses, ttl := dnsAddresses(reply)
		if len(addresses) == 0 {
			return reply, nil
		}
		ip, e := c.dns.address(name)
		if e != nil {
			return nil, e
		}
		response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: q.Type, Class: q.Class, TTL: uint32(min(ttl.Seconds(), 60))}, Body: &dnsmessage.AResource{A: ip.As4()}}}
		return response.Pack()
	}
	return reply, nil
}
func (c *Client) udpDNS(conn net.Conn) {
	buf := make([]byte, 4096)
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, e := conn.Read(buf)
		if e != nil {
			return
		}
		out, e := c.answer(buf[:n])
		if e != nil {
			return
		}
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, e = conn.Write(out); e != nil {
			return
		}
	}
}
func (c *Client) tcpDNS(conn net.Conn) {
	for {
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		var h [2]byte
		if _, e := io.ReadFull(conn, h[:]); e != nil {
			return
		}
		n := binary.BigEndian.Uint16(h[:])
		if n < 12 || n > 4096 {
			return
		}
		raw := make([]byte, n)
		if _, e := io.ReadFull(conn, raw); e != nil {
			return
		}
		out, e := c.answer(raw)
		if e != nil {
			return
		}
		binary.BigEndian.PutUint16(h[:], uint16(len(out)))
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, e = conn.Write(append(h[:], out...)); e != nil {
			return
		}
	}
}
