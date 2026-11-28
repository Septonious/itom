// Package relay implements a PSK-authenticated TLS TCP exit without certificate files.
package relay

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"itom.local/mailstream"
	"math/big"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Config contains no externally managed certificate paths.
type Config struct {
	Address        string `json:"address"`
	Listen         string `json:"listen,omitempty"`
	Key            string `json:"key"`
	TimeoutMS      int    `json:"connect_timeout_ms,omitempty"`
	DirectFallback *bool  `json:"direct_fallback,omitempty"`
	RetrySeconds   int    `json:"retry_seconds,omitempty"`
	MaxConnections int    `json:"max_connections,omitempty"`
}

// ErrTarget describes a destination failure, not an unavailable exit.
var ErrTarget = errors.New("exit could not connect to target") //nolint:gochecknoglobals // sentinel
// ai-generated: bounded settings validation before any listener starts.
func Load(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return c, e
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 || info.Mode().Perm() & ^os.FileMode(0640) != 0 {
		return c, errors.New("relay configuration must be a private regular file (0600 or 0640)")
	}
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return c, e
	}
	if len(b) > 65536 {
		return c, errors.New("relay configuration too large")
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, errors.New("invalid relay configuration JSON")
	}
	if _, e = keyBytes(c.Key); e != nil {
		return c, e
	}
	if c.TimeoutMS == 0 {
		c.TimeoutMS = 2500
	}
	if c.RetrySeconds == 0 {
		c.RetrySeconds = 15
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 2048
	}
	if c.TimeoutMS < 100 || c.TimeoutMS > 10000 || c.RetrySeconds < 1 || c.RetrySeconds > 300 || c.MaxConnections < 1 || c.MaxConnections > 65536 {
		return c, errors.New("invalid relay limits")
	}
	for _, a := range []string{c.Address, c.Listen} {
		if a != "" {
			if e = validTarget(a); e != nil {
				return c, e
			}
		}
	}
	return c, nil
}

// ai-generated: accept exactly one random 32-byte hexadecimal secret.
func keyBytes(raw string) ([]byte, error) {
	b, e := hex.DecodeString(raw)
	if e != nil || len(b) != 32 {
		return nil, errors.New("relay key must contain 64 hex characters")
	}
	return b, nil
}

// ai-generated: domain-separated identities authenticate both roles using the shared secret.
func identity(key []byte, role string) ed25519.PrivateKey {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte("olcrtc-relay-v1/" + role))
	return ed25519.NewKeyFromSeed(h.Sum(nil))
}

// ai-generated: standard TLS 1.3 with mandatory role-specific public-key pinning.
func policy(c Config, server bool) (*tls.Config, error) {
	key, e := keyBytes(c.Key)
	if e != nil {
		return nil, e
	}
	role, peer := "client", "server"
	if server {
		role, peer = peer, role
	}
	private := identity(key, role)
	expected := identity(key, peer).Public().(ed25519.PublicKey)
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return nil, e
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "olcrtc-relay-v1-" + role}, NotBefore: time.Unix(0, 0), NotAfter: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if e != nil {
		return nil, e
	}
	p := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}, NextProtos: []string{relayProtocolV2}, SessionTicketsDisabled: true}
	p.InsecureSkipVerify = true //nolint:gosec // mandatory VerifyConnection pins the PSK-derived identity, no public CA or hostname.
	p.VerifyConnection = func(cs tls.ConnectionState) error {
		if cs.NegotiatedProtocol != relayProtocolV2 || len(cs.PeerCertificates) != 1 {
			return errors.New("relay authentication failed")
		}
		pub, ok := cs.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
		if !ok || subtle.ConstantTimeCompare(pub, expected) != 1 {
			return errors.New("relay key mismatch")
		}
		return nil
	}
	if server {
		p.ClientAuth = tls.RequireAnyClientCert
	}
	return p, nil
}

// Dialer shares only TLS settings and cooldown state, never destination TCP streams.
type Dialer struct {
	cfg               Config
	tls               *tls.Config
	mu                sync.Mutex
	retry             time.Time
	creditConnections atomic.Uint64
	Log               func(string)
}

// ai-generated: prepare a reusable authenticated dialer.
func New(c Config) (*Dialer, error) {
	if e := validTarget(c.Address); e != nil {
		return nil, e
	}
	p, e := policy(c, false)
	if e != nil {
		return nil, e
	}
	if c.TimeoutMS == 0 {
		c.TimeoutMS = 2500
	}
	if c.RetrySeconds == 0 {
		c.RetrySeconds = 15
	}
	return &Dialer{cfg: c, tls: p}, nil
}

// Direct fallback must be explicitly enabled. No user payload is replayed.
func (d *Dialer) Dial(ctx context.Context, target string, direct func(context.Context, string) (net.Conn, error)) (net.Conn, error) {
	d.mu.Lock()
	try := !time.Now().Before(d.retry)
	d.mu.Unlock()
	if try {
		conn, e := d.Connect(ctx, target)
		if e == nil {
			d.mu.Lock()
			recovered := !d.retry.IsZero()
			d.retry = time.Time{}
			d.mu.Unlock()
			if recovered && d.Log != nil {
				d.Log("exit recovered; new connections use exit")
			}
			return conn, nil
		}
		if !errors.Is(e, ErrTarget) {
			d.mu.Lock()
			first := !time.Now().Before(d.retry)
			d.retry = time.Now().Add(time.Duration(d.cfg.RetrySeconds) * time.Second)
			d.mu.Unlock()
			if first && d.Log != nil {
				d.Log("event=exit_unavailable retry_cooldown=true")
			}
		}
		if d.cfg.DirectFallback == nil || !*d.cfg.DirectFallback {
			return nil, e
		}
	}
	if d.cfg.DirectFallback == nil || !*d.cfg.DirectFallback {
		return nil, errors.New("exit unavailable; direct fallback disabled")
	}
	return direct(ctx, target)
}

// ai-generated: authenticate one TLS connection and wait for a bounded destination acknowledgement.
func (d *Dialer) Connect(ctx context.Context, target string) (net.Conn, error) {
	if e := validTarget(target); e != nil {
		return nil, e
	}
	setup, cancel := context.WithTimeout(ctx, time.Duration(d.cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	raw, e := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(setup, "tcp", d.cfg.Address)
	if e != nil {
		return nil, fmt.Errorf("exit dial: %w", e)
	}
	// Credit framing is mandatory on every bearer; reject legacy peers.
	conn := tls.Client(raw, d.tls)
	ok := false
	defer func() {
		if !ok {
			_ = conn.Close()
		}
	}()
	deadline, _ := setup.Deadline()
	_ = conn.SetDeadline(deadline)
	if e = conn.HandshakeContext(setup); e != nil {
		return nil, fmt.Errorf("exit TLS: %w", e)
	}
	frame := make([]byte, 2+len(target))
	binary.BigEndian.PutUint16(frame, uint16(len(target)))
	copy(frame[2:], target)
	if _, e = io.Copy(conn, bytes.NewReader(frame)); e != nil {
		return nil, e
	}
	var ack [1]byte
	if _, e = io.ReadFull(conn, ack[:]); e != nil {
		return nil, fmt.Errorf("exit acknowledgement: %w", e)
	}
	if ack[0] != 0 {
		return nil, ErrTarget
	}
	_ = conn.SetDeadline(time.Time{})
	ok = true
	d.creditConnections.Add(1)
	return newCreditConn(conn), nil
}

// ai-generated: reject invalid ports and oversized destination frames.
func validTarget(s string) error {
	h, p, e := net.SplitHostPort(s)
	if e != nil {
		return e
	}
	n, e := strconv.Atoi(p)
	if e != nil || n < 1 || n > 65535 || h == "" || len(h) > 253 {
		return errors.New("invalid relay endpoint")
	}
	return nil
}

// ai-generated: limit simultaneous handshakes and close all streams on process cancellation.
func Serve(parent context.Context, c Config, ln net.Listener) error {
	return ServeMeasured(parent, c, ln, nil)
}

// Metrics count bytes at the foreign destination socket, excluding TLS framing.
// They are transport counters, not remote-application delivery acknowledgements.
type Metrics struct {
	upload, download atomic.Uint64
	active           atomic.Int64
}
type Snapshot struct {
	UploadBytes, DownloadBytes uint64
	ActiveConnections          int64
}

func (m *Metrics) Snapshot() Snapshot {
	return Snapshot{m.upload.Load(), m.download.Load(), m.active.Load()}
}
func ServeMeasured(parent context.Context, c Config, ln net.Listener, m *Metrics) error {
	return serve(parent, c, ln, mailstream.PublicDial, m)
}

type targetDial func(context.Context, string, string) (net.Conn, error)
type measuredWriter struct {
	io.Writer
	counter *atomic.Uint64
}

func (w measuredWriter) Write(p []byte) (int, error) {
	n, e := w.Writer.Write(p)
	w.counter.Add(uint64(n))
	return n, e
}

type measuredReader struct {
	io.Reader
	counter *atomic.Uint64
}

func (r measuredReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	r.counter.Add(uint64(n))
	return n, e
}

// ServeWithDial allows a controlled in-process fixture to restrict destinations
// to its generated local origins. The production exit command always calls
// Serve, which enforces PublicDial and cannot configure a private target bypass.
func ServeWithDial(parent context.Context, c Config, ln net.Listener, dial targetDial) error {
	return serve(parent, c, ln, dial, nil)
}
func serve(parent context.Context, c Config, ln net.Listener, dial targetDial, m *Metrics) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer ln.Close()
	p, e := policy(c, true)
	if e != nil {
		return e
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	limit := c.MaxConnections
	if limit == 0 {
		limit = 2048
	}
	slots := make(chan struct{}, limit)
	var wg sync.WaitGroup
	defer func() { cancel(); wg.Wait() }()
	for {
		raw, e := ln.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case slots <- struct{}{}:
		default:
			_ = raw.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-slots }(); serveConn(ctx, tls.Server(raw, p), dial, m) }()
	}
}

// ai-generated: authenticate before target selection, preserving half-closes and bounded shutdown.
func serveConn(ctx context.Context, c *tls.Conn, dial targetDial, m *Metrics) {
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	if c.HandshakeContext(ctx) != nil {
		return
	}
	var h [2]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return
	}
	n := int(binary.BigEndian.Uint16(h[:]))
	if n < 1 || n > 1024 {
		return
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(c, b); e != nil {
		return
	}
	if validTarget(string(b)) != nil {
		return
	}
	setup, cancel := context.WithTimeout(ctx, 5*time.Second)
	out, e := dial(setup, "tcp", string(b))
	cancel()
	if e != nil {
		_, _ = c.Write([]byte{1})
		return
	}
	defer out.Close()
	// All credit-negotiated streams need bounded origin-side
	// read-ahead. Cap the socket before acknowledging the destination so
	// a fast origin cannot accumulate a multi-megabyte receive queue.
	if tcp, ok := out.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(128 << 10)
	}
	var upload io.Writer = out
	var download io.Reader = out
	if m != nil {
		m.active.Add(1)
		defer m.active.Add(-1)
		upload = measuredWriter{out, &m.upload}
		download = measuredReader{out, &m.download}
	}
	stopOut := context.AfterFunc(ctx, func() { _ = out.Close() })
	defer stopOut()
	if _, e = c.Write([]byte{0}); e != nil {
		return
	}
	_ = c.SetDeadline(time.Time{})
	var link net.Conn = newCreditConn(c)
	defer link.Close()
	done := make(chan struct{})
	go func() {
		_, e := io.Copy(upload, link)
		if e != nil {
			_ = out.Close()
		} else if half, ok := out.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
		close(done)
	}()
	_, _ = io.Copy(link, download)
	_ = link.(interface{ CloseWrite() error }).CloseWrite()
	_ = link.SetReadDeadline(time.Now().Add(30 * time.Second))
	<-done
}

// Negotiated connection counts are diagnostic only, not a scheduling signal.
func (d *Dialer) NegotiationCounts() map[string]uint64 {
	return map[string]uint64{"credit": d.creditConnections.Load(), "legacy": 0}
}
