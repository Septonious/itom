package mobile

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const primaryDNS = "31.131.251.83"
const backupDNS = "185.22.234.229"
const maxDNSBytes = 4096
const directDNSTimeout = 250 * time.Millisecond

type dnsSettings struct {
	Primary      string `json:"primary"`
	Backup       string `json:"backup"`
	ITOMFallback bool   `json:"itom_fallback"`
}

func defaultDNSSettings() dnsSettings {
	return dnsSettings{Primary: primaryDNS, Backup: backupDNS}
}
func (s dnsSettings) validate() error {
	for i, value := range []string{s.Primary, s.Backup} {
		if value == "" && i == 1 {
			continue
		}
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() || ip.Unmap() == netip.AddrFrom4([4]byte{255, 255, 255, 255}) {
			return errors.New("DNS_ADDRESS")
		}
	}
	primary, _ := netip.ParseAddr(s.Primary)
	backup, _ := netip.ParseAddr(s.Backup)
	if backup.IsValid() && primary.Unmap() == backup.Unmap() {
		return errors.New("DNS_DUPLICATE")
	}
	if s.ITOMFallback && s.Backup == "" {
		return errors.New("DNS_BACKUP_REQUIRED")
	}
	return nil
}

type dnsDial func(context.Context, string, string) (net.Conn, error)
type dnsCacheEntry struct {
	ips   []netip.Addr
	until time.Time
}
type dnsResolver struct {
	settings dnsSettings
	direct   dnsDial // must protect and bind every UDP/TCP socket before connect
	tunnel   dnsDial // used ONLY when explicitly configured
	mu       sync.Mutex
	cache    map[string]dnsCacheEntry
	imap     atomic.Bool // scoped to one native/VPN session; never reset on line reconnect
}

func newDNSResolver(s dnsSettings, direct, tunnel dnsDial) (*dnsResolver, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if direct == nil || s.ITOMFallback && tunnel == nil {
		return nil, errors.New("DNS_DIALER")
	}
	return &dnsResolver{settings: s, direct: direct, tunnel: tunnel, cache: make(map[string]dnsCacheEntry)}, nil
}

func dnsQuestion(host string) ([]byte, error) {
	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, errors.New("DNS_NAME")
	}
	return (&dnsmessage.Message{Header: dnsmessage.Header{RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}).Pack()
}

func validDNSReply(query, reply []byte) bool {
	var q, a dnsmessage.Message
	if len(reply) > maxDNSBytes || q.Unpack(query) != nil || a.Unpack(reply) != nil || !a.Response || a.ID != q.ID || a.OpCode != q.OpCode || len(a.Questions) != 1 || len(q.Questions) != 1 {
		return false
	}
	return strings.EqualFold(a.Questions[0].Name.String(), q.Questions[0].Name.String()) && a.Questions[0].Type == q.Questions[0].Type && a.Questions[0].Class == q.Questions[0].Class
}
func usableDNSReply(query, reply []byte) bool {
	return validDNSReply(query, reply) && reply[2]&2 == 0 && reply[3]&15 != 2 && reply[3]&15 != 5
}

func (r *dnsResolver) exchange(ctx context.Context, raw []byte) ([]byte, error) {
	var query dnsmessage.Message
	if len(raw) > maxDNSBytes || query.Unpack(raw) != nil || query.Response || query.OpCode != 0 || len(query.Questions) != 1 {
		return nil, errors.New("DNS_QUERY")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	packet := append([]byte(nil), raw...)
	if _, err := rand.Read(packet[:2]); err != nil {
		return nil, err
	}
	var reply []byte
	var err error
	if r.imap.Load() {
		reply, err = r.imapQuery(ctx, packet)
	} else {
		reply, err = r.directRace(ctx, packet)
		if err != nil && r.settings.ITOMFallback && ctx.Err() == nil {
			r.imap.Store(true)
			reply, err = r.imapQuery(ctx, packet)
		}
	}
	if err != nil || !validDNSReply(packet, reply) {
		return nil, errors.New("DNS_RESOLVER_FAILED")
	}
	copy(reply[:2], raw[:2])
	r.remember(reply)
	return reply, nil
}

func (r *dnsResolver) directRace(ctx context.Context, raw []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, directDNSTimeout)
	type result struct {
		body []byte
		err  error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	count := 0
	for _, host := range []string{r.settings.Primary, r.settings.Backup} {
		if host == "" {
			continue
		}
		count++
		workers.Go(func() {
			address := net.JoinHostPort(host, "53")
			body, err := dnsSocketExchange(ctx, r.direct, "udp", address, raw)
			if err == nil && len(body) >= 12 && binary.BigEndian.Uint16(body) == binary.BigEndian.Uint16(raw) && body[2]&2 != 0 {
				body, err = dnsSocketExchange(ctx, r.direct, "tcp", address, raw)
			}
			results <- result{body, err}
		})
	}
	for range count {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-results:
			if result.err == nil && usableDNSReply(raw, result.body) {
				return result.body, nil
			}
		}
	}
	return nil, errors.New("DNS_RESOLVER_FAILED")
}

func (r *dnsResolver) imapQuery(ctx context.Context, raw []byte) ([]byte, error) {
	for _, host := range []string{r.settings.Primary, r.settings.Backup} {
		if host == "" {
			continue
		}
		call, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		address := net.JoinHostPort(host, "53")
		reply, err := dnsSocketExchange(call, r.tunnel, "tcp", address, raw)
		cancel()
		if err == nil && usableDNSReply(raw, reply) {
			return reply, nil
		}
	}
	return nil, errors.New("DNS_RESOLVER_FAILED")
}

func dnsSocketExchange(ctx context.Context, dial dnsDial, network, address string, raw []byte) ([]byte, error) {
	conn, err := dial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if network == "udp" {
		if _, err = conn.Write(raw); err != nil {
			return nil, err
		}
		body := make([]byte, maxDNSBytes+1)
		n, err := conn.Read(body)
		return body[:n], err
	}
	packet := binary.BigEndian.AppendUint16(nil, uint16(len(raw)))
	packet = append(packet, raw...)
	if _, err = io.Copy(conn, bytes.NewReader(packet)); err != nil {
		return nil, err
	}
	var header [2]byte
	if _, err = io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(header[:]))
	if n < 12 || n > maxDNSBytes {
		return nil, errors.New("DNS_SIZE")
	}
	reply := make([]byte, n)
	_, err = io.ReadFull(conn, reply)
	return reply, err
}

func dnsAddresses(raw []byte) ([]netip.Addr, time.Duration) {
	var m dnsmessage.Message
	if m.Unpack(raw) != nil || len(m.Questions) != 1 || m.RCode != dnsmessage.RCodeSuccess || m.Truncated || m.Questions[0].Type != dnsmessage.TypeA || m.Questions[0].Class != dnsmessage.ClassINET {
		return nil, 0
	}
	name := strings.ToLower(m.Questions[0].Name.String())
	ttl := uint32(3600)
	for range 16 {
		changed := false
		for _, a := range m.Answers {
			if c, ok := a.Body.(*dnsmessage.CNAMEResource); ok && a.Header.Class == dnsmessage.ClassINET && strings.ToLower(a.Header.Name.String()) == name {
				name, ttl, changed = strings.ToLower(c.CNAME.String()), min(ttl, a.Header.TTL), true
				break
			}
		}
		if !changed {
			var ips []netip.Addr
			for _, a := range m.Answers {
				if address, ok := a.Body.(*dnsmessage.AResource); ok && a.Header.Class == dnsmessage.ClassINET && strings.ToLower(a.Header.Name.String()) == name && len(ips) < 16 {
					ips = append(ips, netip.AddrFrom4(address.A))
					ttl = min(ttl, a.Header.TTL)
				}
			}
			return ips, time.Duration(ttl) * time.Second
		}
	}
	return nil, 0
}
func (r *dnsResolver) remember(raw []byte) {
	ips, ttl := dnsAddresses(raw)
	if len(ips) == 0 || ttl == 0 {
		return
	}
	var m dnsmessage.Message
	m.Unpack(raw)
	name := strings.ToLower(strings.TrimSuffix(m.Questions[0].Name.String(), "."))
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.cache) >= 1024 {
		clear(r.cache)
	}
	r.cache[name] = dnsCacheEntry{ips: ips, until: time.Now().Add(ttl)}
}
func (r *dnsResolver) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	r.mu.Lock()
	cached, ok := r.cache[name]
	r.mu.Unlock()
	if ok && time.Now().Before(cached.until) {
		return append([]netip.Addr(nil), cached.ips...), nil
	}
	query, err := dnsQuestion(name)
	if err != nil {
		return nil, err
	}
	reply, err := r.exchange(ctx, query)
	if err != nil {
		return nil, err
	}
	ips, _ := dnsAddresses(reply)
	if len(ips) == 0 {
		return nil, errors.New("DNS_NO_IPV4")
	}
	return ips, nil
}
