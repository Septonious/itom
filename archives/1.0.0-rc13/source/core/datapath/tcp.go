package datapath

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/adminapi"
	"itom.local/core/session"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrExitPolicy = errors.New("TCP_EXIT_POLICY")
var blockedIPv4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
}

func allowedIP(ip netip.Addr, private bool) bool {
	if !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if private {
		return true
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blockedIPv4 {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type ExitConfig struct {
	InternalDial                   func(context.Context, wire.Connect) (*net.TCPConn, bool, error)
	Generation                     uint64
	MaxSockets, MaxDials, Metadata int
	AllowPrivate                   bool
	Resolver                       Resolver
}
type DialResult struct {
	token      session.DialToken
	conn       *net.TCPConn
	reason     wire.Reason
	claimed    *sync.Once
	diagnostic DialDiagnostic
}

// DialDiagnostic distinguishes target failures from mailbox carrier failures.
// No hostname, resolved IP or provider reply is retained.
type DialDiagnostic struct {
	Stage     string `json:"stage"`
	Reason    string `json:"reason"`
	SlotMS    int64  `json:"slot_ms"`
	DNSMS     int64  `json:"dns_ms"`
	ConnectMS int64  `json:"connect_ms"`
	Port      uint16 `json:"port"`
}

func (r DialResult) Diagnostic() DialDiagnostic { return r.diagnostic }

// TCPExit owns the OPEN replay book and adopted sockets. DNS is resolved once,
// each selected IP is checked, then the numeric address is dialled: no re-resolve.
// Engine.Activate must happen only after Complete returns an adopted socket.
type TCPExit struct {
	mu      sync.Mutex
	cfg     ExitConfig
	book    *session.OpenBook
	sockets map[uint64]*net.TCPConn
	jobs    map[session.DialToken]session.DialJob
	started map[session.DialToken]bool
	slots   chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	stopped bool
}

func NewTCPExit(parent context.Context, c ExitConfig) (*TCPExit, error) {
	if c.Generation == 0 || c.MaxSockets < 1 || c.MaxSockets > 128 || c.MaxDials < 1 || c.MaxDials > c.MaxSockets {
		return nil, ErrState
	}
	if c.Resolver == nil {
		c.Resolver = net.DefaultResolver
	}
	policy := session.DefaultControlPolicy()
	policy.Timeout = 15 * time.Second
	b, err := session.NewOpenBook(c.Generation, c.MaxSockets, c.Metadata, policy)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	return &TCPExit{cfg: c, book: b, sockets: map[uint64]*net.TCPConn{}, jobs: map[session.DialToken]session.DialJob{}, started: map[session.DialToken]bool{}, slots: make(chan struct{}, c.MaxDials), ctx: ctx, cancel: cancel}, nil
}
func (x *TCPExit) Request(now time.Duration, f wire.Frame) (session.OpenAction, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	a, err := x.book.Receive(x.cfg.Generation, now, f)
	if err == nil && a.Dial != nil {
		x.jobs[a.Dial.Token] = *a.Dial
	}
	return a, err
}

// Dial runs on a bounded worker, not while holding the actor/book lock.
func (x *TCPExit) Dial(job session.DialJob) DialResult {
	result := DialResult{token: job.Token, reason: wire.ReasonNetwork, claimed: new(sync.Once)}
	result.diagnostic = DialDiagnostic{Stage: "admission", Port: job.Target.Port}
	x.mu.Lock()
	expected, exists := x.jobs[job.Token]
	valid := exists && expected == job && !x.started[job.Token] && !x.stopped
	if valid {
		x.started[job.Token] = true
	}
	x.mu.Unlock()
	if !valid || job.Token.Generation != x.cfg.Generation {
		result.reason = wire.ReasonClosed
		// A rejected duplicate is not a completion of the original job. In
		// particular it must not terminalize OPEN before the real dial returns.
		result.claimed = nil
		return result
	}
	if _, err := wire.EncodeControl(job.Token.Stream, job.Target); err != nil {
		result.reason = wire.ReasonProtocol
		return result
	}
	ctx, cancel := context.WithTimeout(x.ctx, 15*time.Second)
	defer cancel()
	started := time.Now()
	select {
	case x.slots <- struct{}{}:
		defer func() { <-x.slots }()
	case <-ctx.Done():
		result.reason = wire.ReasonTimeout
		result.diagnostic.Reason = "timeout"
		result.diagnostic.SlotMS = time.Since(started).Milliseconds()
		return result
	}
	result.diagnostic.SlotMS = time.Since(started).Milliseconds()
	// Only the explicitly reserved target can enter this server-supplied hook.
	// Ordinary exits retain the same resolver and private-address policy.
	if strings.EqualFold(job.Target.Host, adminapi.Host) {
		result.reason = wire.ReasonPolicy
		result.diagnostic.Stage = "management"
		result.diagnostic.Reason = "denied"
		if job.Target.Port != adminapi.Port || x.cfg.InternalDial == nil {
			return result
		}
		conn, handled, err := x.cfg.InternalDial(ctx, job.Target)
		if err != nil || !handled || conn == nil || ctx.Err() != nil {
			if conn != nil {
				conn.Close()
			}
			return result
		}
		result.conn = conn
		result.reason = wire.ReasonNone
		result.diagnostic.Reason = "ready"
		return result
	}
	result.diagnostic.Stage = "dns"
	started = time.Now()
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(job.Target.Host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		var err error
		ips, err = x.cfg.Resolver.LookupNetIP(ctx, "ip4", job.Target.Host)
		result.diagnostic.DNSMS = time.Since(started).Milliseconds()
		if err != nil {
			result.diagnostic.Reason = targetDNSFailureReason(err)
			if ctx.Err() != nil {
				result.reason = wire.ReasonTimeout
				result.diagnostic.Reason = "timeout"
			}
			return result
		}
	}
	if len(ips) == 0 || len(ips) > 16 {
		result.diagnostic.Reason = "policy"
		result.reason = wire.ReasonPolicy
		return result
	}
	for _, ip := range ips {
		// net.Resolver may return an A record as IPv4-mapped IPv6. Normalize
		// resolver output before the IPv4 policy/dial checks; mapped private or
		// reserved addresses still undergo exactly the same address policy.
		ip = ip.Unmap()
		if !allowedIP(ip, x.cfg.AllowPrivate) {
			result.diagnostic.Stage, result.diagnostic.Reason = "policy", "blocked"
			result.reason = wire.ReasonPolicy
			continue
		}
		result.diagnostic.Stage = "connect"
		started = time.Now()
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), strconv.Itoa(int(job.Target.Port))))
		result.diagnostic.ConnectMS += time.Since(started).Milliseconds()
		if err != nil {
			if ctx.Err() != nil {
				result.reason = wire.ReasonTimeout
				result.diagnostic.Reason = "timeout"
				return result
			}
			result.reason = wire.ReasonRefused
			result.diagnostic.Reason = "failed"
			continue
		}
		tcp, ok := conn.(*net.TCPConn)
		if !ok {
			conn.Close()
			return result
		}
		if ctx.Err() != nil {
			tcp.Close()
			result.reason = wire.ReasonTimeout
			return result
		}
		result.conn = tcp
		result.reason = wire.ReasonNone
		result.diagnostic.Stage, result.diagnostic.Reason = "ready", "ok"
		return result
	}
	return result
}

// Complete adopts at most one socket. Every stale/duplicate/late success closes
// its unadopted socket. The owner must call Complete for every Dial result.
func (x *TCPExit) Complete(now time.Duration, r DialResult) (session.OpenAction, *net.TCPConn, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r.claimed == nil {
		return session.OpenAction{}, nil, ErrState
	}
	claimed := false
	r.claimed.Do(func() { claimed = true })
	if !claimed {
		return session.OpenAction{}, nil, ErrState
	}
	a, err := x.book.Complete(r.token, now, r.reason)
	if err != nil || !a.Adopt || x.stopped {
		if r.conn != nil {
			r.conn.Close()
		}
		return a, nil, err
	}
	if r.conn == nil {
		return session.OpenAction{}, nil, ErrState
	}
	x.sockets[r.token.Stream] = r.conn
	return a, r.conn, nil
}
func (x *TCPExit) Retire(id uint64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if c := x.sockets[id]; c != nil {
		c.Close()
		delete(x.sockets, id)
	}
	token := session.DialToken{Generation: x.cfg.Generation, Stream: id}
	delete(x.jobs, token)
	delete(x.started, token)
	return x.book.Retire(x.cfg.Generation, id)
}
func (x *TCPExit) Stop() {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.stopped {
		return
	}
	x.stopped = true
	x.cancel()
	x.book.Stop()
	clear(x.jobs)
	clear(x.started)
	for id, c := range x.sockets {
		c.Close()
		delete(x.sockets, id)
	}
}

// SocketWrite is a bounded worker operation. The caller feeds N into Delivered
// even when Err is non-nil, then terminates that stream/session on error.
// Only one write worker may own a given socket at a time.
type WriteResult struct {
	N   int
	Err error
}

func WriteSocket(ctx context.Context, conn net.Conn, data []byte) WriteResult {
	if len(data) == 0 || len(data) > wire.MaxData {
		return WriteResult{Err: ErrState}
	}
	if err := ctx.Err(); err != nil {
		return WriteResult{Err: err}
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return WriteResult{Err: err}
	}
	n, err := conn.Write(data)
	if n < 0 || n > len(data) {
		return WriteResult{Err: fmt.Errorf("INVALID_SOCKET_WRITE")}
	}
	if n == 0 && err == nil {
		err = errors.New("SOCKET_NO_PROGRESS")
	}
	return WriteResult{N: n, Err: err}
}

// Half-close must be called only after Engine.HalfClosed is true; receiving FIN
// alone is insufficient when DATA is reordered or a socket write was partial.
func HalfCloseSocket(conn *net.TCPConn) error { return conn.CloseWrite() }

// Keep target DNS failures actionable without retaining names, addresses or
// resolver error strings. The OPEN wire reason remains compatible with peers.
func targetDNSFailureReason(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		if dns.IsTimeout {
			return "timeout"
		}
		if dns.IsNotFound {
			return "not_found"
		}
		if dns.IsTemporary {
			return "temporary"
		}
	}
	return "failed"
}
