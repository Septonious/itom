package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"itom.local/core/relay"
	"net"
	"strconv"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

func serveFixture(ctx context.Context, cfg servercfg.Config, dir, origin, echo string, encrypted bool, exitRTT time.Duration) error {
	if encrypted {
		return serveEncryptedFixture(ctx, cfg, dir, origin, echo, exitRTT)
	}
	return tunnel.ServeManaged(ctx, cfg, dir, nil, func(call context.Context, _ servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
		address := net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
		for _, allowed := range []struct{ virtual, actual string }{{"203.0.113.80", origin}, {"203.0.113.80", echo}} {
			_, port, _ := net.SplitHostPort(allowed.actual)
			if target.Host == allowed.virtual && strconv.Itoa(int(target.Port)) == port {
				address = allowed.actual
			}
		}
		if address != origin && address != echo {
			return nil, true, errors.New("fixture target denied")
		}
		conn, err := (&net.Dialer{}).DialContext(call, "tcp", address)
		if err != nil {
			return nil, true, err
		}
		return conn.(*net.TCPConn), true, nil
	})
}

// Two independently authenticated TLS endpoints, same tunnel server and native
// TCP frontend. Only this fixture can dial its generated loopback origins.
func serveEncryptedFixture(ctx context.Context, cfg servercfg.Config, dir, origin, echo string, exitRTT time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer ln.Close()
	var key [32]byte
	if _, e = rand.Read(key[:]); e != nil {
		return e
	}
	address := ln.Addr().String()
	if exitRTT > 0 {
		proxy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer proxy.Close()
		go relayPropagation(ctx, proxy, address, exitRTT/2)
		address = proxy.Addr().String()
	}
	rc := relay.Config{Address: address, Key: hex.EncodeToString(key[:])}
	d, e := relay.New(rc)
	if e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() {
		done <- relay.ServeWithDial(ctx, rc, ln, func(call context.Context, network, address string) (net.Conn, error) {
			host, port, e := net.SplitHostPort(address)
			if e != nil {
				return nil, e
			}
			if host == "203.0.113.80" {
				address = net.JoinHostPort("127.0.0.1", port)
			}
			if network != "tcp" || address != origin && address != echo {
				return nil, errors.New("fixture relay target denied")
			}
			return (&net.Dialer{}).DialContext(call, network, address)
		})
	}()
	e = tunnel.ServeManagedWithExit(ctx, cfg, dir, nil, nil, d)
	cancel()
	return errors.Join(e, <-done)
}

// A separate propagation hop between the local RU and encrypted exit fixtures.
// The bounded pipeline preserves the order and half-close of TCP traffic while
// allowing bytes written during the one-way propagation interval to coexist.
func relayPropagation(ctx context.Context, listener net.Listener, target string, delay time.Duration) {
	for {
		a, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer a.Close()
			b, err := (&net.Dialer{}).DialContext(ctx, "tcp", target)
			if err != nil {
				return
			}
			defer b.Close()
			life, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := context.AfterFunc(life, func() { a.Close(); b.Close() })
			defer stop()
			done := make(chan error, 2)
			go func() { done <- propagate(life, b, a, delay) }()
			go func() { done <- propagate(life, a, b, delay) }()
			if err := <-done; err != nil {
				cancel()
			}
			<-done
		}()
	}
}
func propagate(ctx context.Context, dst, src net.Conn, delay time.Duration) error {
	type chunk struct {
		body []byte
		at   time.Time
		err  error
	}
	q := make(chan chunk, 128)
	go func() {
		defer close(q)
		for {
			buf := make([]byte, 32<<10)
			n, err := src.Read(buf)
			select {
			case q <- chunk{buf[:n], time.Now().Add(delay), err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for p := range q {
		timer := time.NewTimer(max(0, time.Until(p.at)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if len(p.body) > 0 {
			if _, err := io.Copy(dst, bytes.NewReader(p.body)); err != nil {
				return err
			}
		}
		if p.err != nil {
			if p.err == io.EOF {
				if tcp, ok := dst.(*net.TCPConn); ok {
					return tcp.CloseWrite()
				}
				return nil
			}
			return p.err
		}
	}
	return nil
}
