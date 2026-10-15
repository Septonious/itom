// Package socks is a loopback-only SOCKS5 CONNECT frontend. It never resolves
// destinations or dials them directly: only the authenticated transport may Open.
package socks

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrProtocol = errors.New("SOCKS_PROTOCOL")
var ErrConfig = errors.New("SOCKS_CONFIG")

// Stream.Close must unblock Read and Write; CloseWrite sends the transport FIN
// after queued bytes. Open returns only after authenticated remote OPEN_OK.
type Stream interface {
	io.Reader
	io.Writer
	io.Closer
	CloseWrite() error
}
type OpenFunc func(context.Context, wire.Connect) (Stream, netip.AddrPort, error)
type OpenError struct{ Code wire.Reason }

func (e OpenError) Error() string { return "SOCKS_OPEN_FAILED" }

type Config struct {
	MaxConnections                int
	HandshakeTimeout, OpenTimeout time.Duration
	Auth                          *Credentials
}

func DefaultConfig() Config {
	return Config{MaxConnections: 128, HandshakeTimeout: 10 * time.Second, OpenTimeout: 45 * time.Second}
}
func (c Config) valid() bool {
	return c.MaxConnections > 0 && c.MaxConnections <= 128 && c.HandshakeTimeout > 0 && c.HandshakeTimeout <= time.Minute && c.OpenTimeout > 0 && c.OpenTimeout <= 2*time.Minute
}

// Serve owns listener and accepted sockets. Refuses wildcard/non-loopback binds.
// Cancellation closes listener and all streams, then joins all handlers.
func Serve(ctx context.Context, l net.Listener, c Config, open OpenFunc) error {
	if !c.valid() || open == nil {
		return ErrConfig
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.IsLoopback() {
		return ErrConfig
	}
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	defer l.Close()
	stop := context.AfterFunc(run, func() { l.Close() })
	defer stop()
	slots := make(chan struct{}, c.MaxConnections)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer func() { <-slots }(); _ = Handle(run, conn, c, open) }()
	}
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n < 0 || n > len(b) {
			return io.ErrShortWrite
		}
		b = b[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
func reply(c io.Writer, code byte, bound netip.AddrPort) error {
	p := []byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}
	if bound.IsValid() {
		a := bound.Addr().Unmap()
		if a.Is4() {
			b := a.As4()
			copy(p[4:8], b[:])
		} else {
			p = make([]byte, 22)
			copy(p, []byte{5, code, 0, 4})
			b := a.As16()
			copy(p[4:20], b[:])
		}
		binary.BigEndian.PutUint16(p[len(p)-2:], bound.Port())
	}
	return writeAll(c, p)
}
func request(c net.Conn, auth *Credentials) (wire.Connect, error) {
	var h [2]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return wire.Connect{}, e
	}
	if h[0] != 5 || h[1] == 0 {
		return wire.Connect{}, ErrProtocol
	}
	methods := make([]byte, int(h[1]))
	if _, e := io.ReadFull(c, methods); e != nil {
		return wire.Connect{}, e
	}
	method := byte(0)
	if auth != nil {
		method = 2
	}
	accept := false
	for _, m := range methods {
		accept = accept || m == method
	}
	if !accept {
		_ = writeAll(c, []byte{5, 255})
		return wire.Connect{}, ErrProtocol
	}
	if e := writeAll(c, []byte{5, method}); e != nil {
		return wire.Connect{}, e
	}
	if auth != nil {
		if err := authenticate(c, auth); err != nil {
			return wire.Connect{}, err
		}
	}
	var r [4]byte
	if _, e := io.ReadFull(c, r[:]); e != nil {
		return wire.Connect{}, e
	}
	if r[0] != 5 || r[2] != 0 {
		_ = reply(c, 1, netip.AddrPort{})
		return wire.Connect{}, ErrProtocol
	}
	if r[1] != 1 {
		_ = reply(c, 7, netip.AddrPort{})
		return wire.Connect{}, ErrProtocol
	}
	var host string
	switch r[3] {
	case 1:
		var a [4]byte
		if _, e := io.ReadFull(c, a[:]); e != nil {
			return wire.Connect{}, e
		}
		host = netip.AddrFrom4(a).String()
	case 3:
		var n [1]byte
		if _, e := io.ReadFull(c, n[:]); e != nil {
			return wire.Connect{}, e
		}
		b := make([]byte, int(n[0]))
		if _, e := io.ReadFull(c, b); e != nil {
			return wire.Connect{}, e
		}
		host = strings.ToLower(strings.TrimSuffix(string(b), "."))
	case 4:
		// Current ITOM exit is IPv4-only. Do not claim IPv6 CONNECT support.
		_ = reply(c, 8, netip.AddrPort{})
		return wire.Connect{}, ErrProtocol
	default:
		_ = reply(c, 8, netip.AddrPort{})
		return wire.Connect{}, ErrProtocol
	}
	var p [2]byte
	if _, e := io.ReadFull(c, p[:]); e != nil {
		return wire.Connect{}, e
	}
	target := wire.Connect{Host: host, Port: binary.BigEndian.Uint16(p[:])}
	if _, e := wire.EncodeControl(1, target); e != nil {
		_ = reply(c, 8, netip.AddrPort{})
		return wire.Connect{}, ErrProtocol
	}
	return target, nil
}
func errorCode(err error) byte {
	if errors.Is(err, context.DeadlineExceeded) {
		return 6
	}
	var e OpenError
	if errors.As(err, &e) {
		switch e.Code {
		case wire.ReasonPolicy:
			return 2
		case wire.ReasonNetwork:
			return 3
		case wire.ReasonRefused:
			return 5
		case wire.ReasonTimeout:
			return 6
		}
	}
	return 1
}
func Handle(ctx context.Context, c net.Conn, cfg Config, open OpenFunc) error {
	defer c.Close()
	if !cfg.valid() || open == nil {
		return ErrConfig
	}
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if err := c.SetDeadline(time.Now().Add(cfg.HandshakeTimeout)); err != nil {
		return err
	}
	target, err := request(c, cfg.Auth)
	if err != nil {
		return err
	}
	if err = c.SetDeadline(time.Now().Add(cfg.OpenTimeout)); err != nil {
		return err
	}
	opening, cancel := context.WithTimeout(ctx, cfg.OpenTimeout)
	remote, bound, err := open(opening, target)
	expired := opening.Err()
	cancel()
	if err == nil && expired != nil {
		err = expired
	}
	if err != nil || remote == nil {
		if remote != nil {
			remote.Close()
		}
		if err == nil {
			err = ErrProtocol
		}
		_ = reply(c, errorCode(err), netip.AddrPort{})
		return err
	}
	defer remote.Close()
	remoteStop := context.AfterFunc(ctx, func() { remote.Close() })
	defer remoteStop()
	if err = reply(c, 0, bound); err != nil {
		return err
	}
	if err = c.SetDeadline(time.Time{}); err != nil {
		return err
	}
	local, ok := c.(interface{ CloseWrite() error })
	if !ok {
		return ErrConfig
	}
	// Each direction keeps a single 32 KiB buffer. EOF half-closes only that
	// direction; the response can continue until its independent EOF.
	done := make(chan error, 2)
	go func() {
		e := copyBounded(remote, c)
		if e == nil {
			e = remote.CloseWrite()
		}
		done <- e
	}()
	go func() {
		e := copyBounded(c, remote)
		if e == nil {
			e = local.CloseWrite()
		}
		done <- e
	}()
	first := <-done
	if first != nil {
		c.Close()
		remote.Close()
	}
	second := <-done
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(first, second)
}
func copyBounded(dst io.Writer, src io.Reader) error {
	b := make([]byte, wire.MaxData)
	defer clear(b)
	empty := 0
	for {
		n, e := src.Read(b)
		if n < 0 || n > len(b) {
			return io.ErrUnexpectedEOF
		}
		if n > 0 {
			empty = 0
			if err := writeAll(dst, b[:n]); err != nil {
				return err
			}
		} else {
			empty++
			if empty >= 100 {
				return io.ErrNoProgress
			}
		}
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
	}
}

// Credentials contain comparison digests only. RFC1929 wire credentials remain
// local to the loopback connection; neither value is sent to the exit server.
type Credentials struct{ user, password [32]byte }

func (*Credentials) String() string { return "SOCKS credentials (<redacted>)" }
func NewCredentials(user, password string) (*Credentials, error) {
	if len(user) < 1 || len(user) > 255 || len(password) < 1 || len(password) > 255 {
		return nil, ErrConfig
	}
	return &Credentials{sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))}, nil
}
func authenticate(c net.Conn, a *Credentials) error {
	fail := func() error { _ = writeAll(c, []byte{1, 1}); return ErrProtocol }
	var h [2]byte
	if _, e := io.ReadFull(c, h[:]); e != nil {
		return e
	}
	if h[0] != 1 || h[1] == 0 {
		return fail()
	}
	user := make([]byte, int(h[1]))
	defer clear(user)
	if _, e := io.ReadFull(c, user); e != nil {
		return e
	}
	var n [1]byte
	if _, e := io.ReadFull(c, n[:]); e != nil {
		return e
	}
	if n[0] == 0 {
		return fail()
	}
	password := make([]byte, int(n[0]))
	defer clear(password)
	if _, e := io.ReadFull(c, password); e != nil {
		return e
	}
	uh, ph := sha256.Sum256(user), sha256.Sum256(password)
	ok := subtle.ConstantTimeCompare(uh[:], a.user[:]) & subtle.ConstantTimeCompare(ph[:], a.password[:])
	if ok != 1 {
		return fail()
	}
	return writeAll(c, []byte{1, 0})
}

// Endpoint settings are supplied by the encrypted Android vault or CLI owner.
// A bind failure is returned; the port never silently changes and no direct
// network fallback is installed if the transport is unavailable.
type Endpoint struct {
	Port               int
	Authentication     bool
	Username, Password string
}

func (Endpoint) String() string { return "SOCKS endpoint (<redacted>)" }
func ListenAndServe(ctx context.Context, e Endpoint, open OpenFunc) error {
	if e.Port < 1024 || e.Port > 65535 || open == nil {
		return ErrConfig
	}
	cfg := DefaultConfig()
	if e.Authentication {
		a, err := NewCredentials(e.Username, e.Password)
		if err != nil {
			return err
		}
		cfg.Auth = a
	}
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(e.Port)))
	if err != nil {
		return err
	}
	return Serve(ctx, l, cfg, open)
}
