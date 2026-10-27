// Package mobile is the narrow gomobile bridge. Secrets arrive in memory from
// Android Keystore; no command line, intent, log, or plaintext config is used.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	tun "github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"itom.local/core/netroute"
	"itom.local/core/servercfg"
	"itom.local/core/socks"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Host callbacks may execute concurrently and must not touch Android UI state.
type Host interface {
	Protect(fd int) bool
	DNS() string
	Lookup(host string) string
	Route(host string) string
	// Startup receives bounded phase/count snapshots before New returns. False
	// means the user canceled or replaced this VPN attempt.
	Startup(snapshot string) bool
}
type config struct {
	Profile     servercfg.Profile
	DNS         []string
	Socks       socks.Endpoint
	Diagnostics bool
	DNSResolver *dnsSettings
}
type Client struct {
	admin    adminClientState
	ctx      context.Context
	cancel   context.CancelFunc
	host     Host
	dialer   *netroute.Dialer
	pool     *tunnel.Client
	mu       sync.Mutex
	closed   bool
	device   device.Device
	stack    *stack.Stack
	wg       sync.WaitGroup
	slots    chan struct{}
	dns      *fakeDNS
	resolver *dnsResolver
	listener net.Listener
	up, down atomic.Uint64
	tcp      atomic.Int64
	failure  atomic.Pointer[string]
	traffic  trafficDiagnostics
}

// New starts provider connections and SOCKS but does not take ownership of TUN.
func New(raw, dir string, host Host) (*Client, error) {
	if host == nil || len(raw) > 1<<20 {
		return nil, errors.New("NATIVE_CONFIG")
	}
	var cfg config
	if e := json.Unmarshal([]byte(raw), &cfg); e != nil {
		return nil, errors.New("NATIVE_CONFIG")
	}
	// VaultStore identifies its in-memory runtime document as Android, whereas
	// the core consumes the canonical server-export profile. Adapt only this
	// known discriminator at the platform boundary; all profile fields still
	// pass the core's original validation before any provider socket is opened.
	if cfg.Profile.Kind == "itom-android" {
		cfg.Profile.Kind = "itom-imap-probe"
	}
	if len(cfg.DNS) == 0 {
		return nil, errors.New("UNDERLYING_DNS_MISSING")
	}
	for _, ip := range cfg.DNS {
		if _, e := netip.ParseAddr(ip); e != nil {
			return nil, errors.New("UNDERLYING_DNS_INVALID")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{ctx: ctx, cancel: cancel, host: host, slots: make(chan struct{}, 128)}
	progress, stopProgress := reportStartup(ctx, cancel, host, 2*len(cfg.Profile.Accounts))
	defer stopProgress()
	control := func(_, _ string, r syscall.RawConn) error {
		ok := false
		e := r.Control(func(fd uintptr) { ok = host.Protect(int(fd)) })
		if e != nil {
			return e
		}
		if !ok {
			return netroute.ErrProtect
		}
		return nil
	}
	// Android's network-scoped resolver supports Private DNS and network changes.
	// Limit outstanding platform calls: getAllByName cannot be canceled by Go.
	lookupSlots := make(chan struct{}, 8)
	c.dialer = &netroute.Dialer{Socket: net.Dialer{Timeout: 20 * time.Second, Control: control}, Lookup: func(call context.Context, name string) ([]netip.Addr, error) {
		select {
		case lookupSlots <- struct{}{}:
		case <-call.Done():
			return nil, call.Err()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result := make(chan string, 1)
		go func() { defer func() { <-lookupSlots }(); result <- host.Lookup(name) }()
		var raw string
		select {
		case raw = <-result:
		case <-call.Done():
			return nil, call.Err()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		var ips []string
		if json.Unmarshal([]byte(raw), &ips) != nil || len(ips) > 64 {
			return nil, errors.New("UNDERLYING_DNS_INVALID")
		}
		out := make([]netip.Addr, 0, len(ips))
		for _, s := range ips {
			if ip, err := netip.ParseAddr(s); err == nil {
				out = append(out, ip)
			}
		}
		return out, nil
	}}
	settings := defaultDNSSettings()
	if cfg.DNSResolver != nil {
		settings = *cfg.DNSResolver
	}
	var resolverErr error
	c.resolver, resolverErr = newDNSResolver(settings, c.dialer.Socket.DialContext, func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || c.pool == nil {
			return nil, errors.New("DNS_TUNNEL_UNAVAILABLE")
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil || port != "53" {
			return nil, errors.New("DNS_ADDRESS")
		}
		stream, _, err := c.pool.Open(ctx, wire.Connect{Host: host, Port: 53})
		if err != nil {
			return nil, err
		}
		conn, ok := stream.(net.Conn)
		if !ok {
			stream.Close()
			return nil, errors.New("DNS_TUNNEL_UNAVAILABLE")
		}
		return conn, nil
	})
	if resolverErr != nil {
		cancel()
		return nil, resolverErr
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		cancel()
		return nil, e
	}
	var e error
	c.dns, e = openDNS(filepath.Join(filepath.Dir(dir), "dns.db"))
	if e != nil {
		cancel()
		return nil, e
	}
	c.pool, e = tunnel.StartClientWithOptions(ctx, cfg.Profile, filepath.Join(dir, "transport"), tunnel.NetworkDial(c.dialer), tunnel.ClientOptions{Diagnostics: cfg.Diagnostics, Progress: progress})
	if e != nil {
		c.Close()
		return nil, e
	}
	sc := socks.DefaultConfig()
	if cfg.Socks.Authentication {
		sc.Auth, e = socks.NewCredentials(cfg.Socks.Username, cfg.Socks.Password)
		if e != nil {
			c.Close()
			return nil, e
		}
	}
	if cfg.Socks.Port < 1024 || cfg.Socks.Port > 65535 {
		c.Close()
		return nil, errors.New("SOCKS_PORT")
	}
	c.listener, e = net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Socks.Port)))
	if e != nil {
		c.Close()
		return nil, errors.New("SOCKS_PORT_IN_USE")
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if e := socks.Serve(ctx, c.listener, sc, c.open); e != nil && ctx.Err() == nil {
			msg := "SOCKS_FAILED"
			c.failure.Store(&msg)
			c.cancel()
		}
	}()
	c.startAdmin(cfg.Profile.User.Key, cfg.Profile.User.ID)
	return c, nil
}

// Attach consumes fd on EVERY path. Java must pass ParcelFileDescriptor.detachFd().
func (c *Client) Attach(fd int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.device != nil {
		closeTUNFD(fd)
		return errors.New("TUN_STATE")
	}
	d, e := fdbased.Open(strconv.Itoa(fd), 1500, 0)
	if e != nil {
		closeTUNFD(fd)
		return e
	}
	s, e := tun.CreateStack(&tun.Config{LinkEndpoint: d, TransportHandler: nativeHandler{c}})
	if e != nil {
		d.Close()
		return e
	}
	c.device = d
	c.stack = s
	return nil
}

// os.File owns a native handle on Windows and a descriptor on Android/Unix.
// This cleanup path must compile for host tests as well as the Android target.
func closeTUNFD(fd int) {
	if f := os.NewFile(uintptr(fd), "itom-tun"); f != nil {
		_ = f.Close()
	}
}
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.cancel()
	if c.listener != nil {
		c.listener.Close()
	}
	if c.stack != nil {
		c.stack.Close()
	}
	if c.device != nil {
		c.device.Close()
	}
	c.mu.Unlock()
	if c.pool != nil {
		c.pool.Close()
	}
	c.wg.Wait()
	if c.dns != nil {
		c.dns.Close()
	}
}
func (c *Client) Stats() string {
	var s tunnel.Stats
	if c.pool != nil {
		s = c.pool.Stats()
		s.TCP = int(c.tcp.Load())
	}
	err := ""
	if c.pool != nil {
		err = c.pool.Failure()
	}
	if e := c.failure.Load(); e != nil {
		err = *e
	}
	b, _ := json.Marshal(struct {
		tunnel.Stats
		Upload, Download uint64
		Error            string
	}{s, c.up.Load(), c.down.Load(), err})
	return string(b)
}

// Snapshot is one observation for UI counters, readiness and diagnostics.
func (c *Client) Snapshot() string {
	var snapshot tunnel.ClientSnapshot
	if c.pool != nil {
		snapshot = c.pool.Snapshot()
	}
	snapshot.TCP = int(c.tcp.Load())
	err := ""
	if c.pool != nil {
		err = c.pool.Failure()
	}
	if failure := c.failure.Load(); failure != nil {
		err = *failure
	}
	raw, _ := json.Marshal(struct {
		tunnel.ClientSnapshot
		Upload, Download uint64
		Error            string
	}{snapshot, c.up.Load(), c.down.Load(), err})
	return string(raw)
}

func (c *Client) worker(conn net.Conn, fn func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		conn.Close()
		return
	}
	select {
	case c.slots <- struct{}{}:
	default:
		c.traffic.workerRefusals.Add(1)
		c.mu.Unlock()
		conn.Close()
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		defer func() { <-c.slots }()
		defer conn.Close()
		stop := context.AfterFunc(c.ctx, func() { conn.Close() })
		defer stop()
		fn()
	}()
}
func (c *Client) open(ctx context.Context, target wire.Connect) (socks.Stream, netip.AddrPort, error) {
	c.traffic.openAttempts.Add(1)
	c.traffic.opening.Add(1)
	stream, addr, err := c.openRoute(ctx, target)
	c.traffic.opening.Add(-1)
	if err != nil {
		c.traffic.openFailures.Add(1)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
			c.traffic.openTimeouts.Add(1)
		}
		return nil, addr, err
	}
	c.traffic.openEstablished.Add(1)
	c.tcp.Add(1)
	return &countStream{Stream: stream, client: c}, addr, nil
}
func (c *Client) openRoute(ctx context.Context, target wire.Connect) (socks.Stream, netip.AddrPort, error) {
	if ip, e := netip.ParseAddr(target.Host); e == nil {
		if !ip.Is4() {
			c.traffic.ipv6Blocked.Add(1)
			return nil, netip.AddrPort{}, errors.New("IPV6_BLOCKED")
		}
		if fakePrefix.Contains(ip) {
			name, e := c.dns.name(ip)
			if e != nil {
				return nil, netip.AddrPort{}, e
			}
			target.Host = name
		}
	}
	route := c.host.Route(target.Host)
	if route != "VPN" && route != "DIRECT" {
		return nil, netip.AddrPort{}, socks.OpenError{Code: wire.ReasonPolicy}
	}
	// Apply domain rules BEFORE substituting the resolver's address. Synthetic
	// DNS keeps this name association stable when multiple domains share an IP.
	if c.resolver != nil {
		if _, err := netip.ParseAddr(target.Host); err != nil {
			addresses, err := c.resolver.lookup(ctx, target.Host)
			if err != nil {
				return nil, netip.AddrPort{}, err
			}
			target.Host = addresses[0].String()
		}
	}
	switch route {
	case "VPN":
		return c.pool.Open(ctx, target)
	case "DIRECT":
		conn, e := c.dialer.DialContext(ctx, "tcp4", net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port))))
		if e != nil {
			return nil, netip.AddrPort{}, e
		}
		tcp, ok := conn.(*net.TCPConn)
		if !ok {
			conn.Close()
			return nil, netip.AddrPort{}, errors.New("DIRECT_SOCKET")
		}
		return tcp, netip.AddrPort{}, nil
	default:
		return nil, netip.AddrPort{}, socks.OpenError{Code: wire.ReasonPolicy}
	}
}
func (c *Client) handleTCP(conn adapter.TCPConn) {
	c.worker(conn, func() {
		id := conn.ID()
		host := id.LocalAddress.String()
		if id.LocalPort == 53 {
			c.tcpDNS(conn)
			return
		}
		ctx, cancel := context.WithTimeout(c.ctx, 45*time.Second)
		upstream, _, e := c.open(ctx, wire.Connect{Host: host, Port: id.LocalPort})
		cancel()
		if e != nil {
			return
		}
		defer upstream.Close()
		stop := context.AfterFunc(c.ctx, func() { upstream.Close() })
		defer stop()
		done := make(chan struct{}, 1)
		go func() {
			defer func() { done <- struct{}{} }()
			n, e, stage := copyApplicationSocket(upstream, conn, make([]byte, 32<<10))
			c.recordCopyStage(upstream, "upload", n, e, stage)
			if e == nil {
				e = upstream.CloseWrite()
				c.recordCopyEnd(upstream, "upload_half_close", 0, e)
			}
			if e != nil {
				upstream.Close()
				conn.Close()
			}
		}()
		n, e, stage := copyApplicationSocket(conn, upstream, make([]byte, 32<<10))
		c.recordCopyStage(upstream, "download", n, e, stage)
		if cw, ok := conn.(interface{ CloseWrite() error }); ok && e == nil {
			closeErr := cw.CloseWrite()
			c.recordCopyEnd(upstream, "download_half_close", 0, closeErr)
		} else {
			conn.Close()
		}
		if e != nil {
			upstream.Close()
		}
		<-done
	})
}
func (c *Client) recordCopyEnd(stream socks.Stream, direction string, n int64, err error) {
	c.recordCopyStage(stream, direction, n, err, "half_close")
}

func (c *Client) recordCopyStage(stream socks.Stream, direction string, n int64, err error, stage string) {
	if c.pool == nil {
		return
	}
	id := uint64(0)
	if counted, ok := stream.(*countStream); ok {
		if identified, ok := counted.Stream.(interface{ StreamID() uint64 }); ok {
			id = identified.StreamID()
		}
	}
	c.pool.RecordApplicationCopyEvent(id, direction, n, err, stage)
}

func (c *Client) handleUDP(conn adapter.UDPConn) {
	if conn.ID().LocalPort != 53 {
		c.traffic.udpBlocked.Add(1)
		conn.Close()
		return
	}
	c.worker(conn, func() { c.udpDNS(conn) })
}

type countStream struct {
	socks.Stream
	client *Client
	once   sync.Once
}

func (s *countStream) Read(b []byte) (int, error) {
	n, e := s.Stream.Read(b)
	s.client.down.Add(uint64(n))
	return n, e
}
func (s *countStream) Write(b []byte) (int, error) {
	n, e := s.Stream.Write(b)
	s.client.up.Add(uint64(n))
	return n, e
}
func (s *countStream) Close() error {
	e := s.Stream.Close()
	s.once.Do(func() { s.client.tcp.Add(-1) })
	return e
}

type nativeHandler struct{ c *Client }

func (h nativeHandler) HandleTCP(conn adapter.TCPConn) { h.c.handleTCP(conn) }
func (h nativeHandler) HandleUDP(conn adapter.UDPConn) { h.c.handleUDP(conn) }

// Diagnostics returns bounded, credential-free provider operation history.
func (c *Client) Diagnostics() string {
	if c.pool == nil {
		return "{}"
	}
	var evidence map[string]json.RawMessage
	if err := json.Unmarshal([]byte(c.pool.Diagnostics()), &evidence); err != nil {
		return "{}"
	}
	evidence["Native"], _ = json.Marshal(c.trafficSnapshot())
	raw, _ := json.Marshal(evidence)
	return string(raw)
}

// RecordNetworkEvent accepts only the core's bounded callback vocabulary.
func (c *Client) RecordNetworkEvent(kind string, handle int64) {
	if c.pool != nil {
		c.pool.RecordNetworkEvent(kind, handle)
	}
}
