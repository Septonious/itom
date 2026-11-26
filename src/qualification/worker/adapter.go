package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"itom.local/core/relay"
	"net"
	"strconv"

	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

func serveFixture(ctx context.Context, cfg servercfg.Config, dir, origin, echo string, encrypted bool) error {
	if encrypted {
		return serveEncryptedFixture(ctx, cfg, dir, origin, echo)
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
func serveEncryptedFixture(ctx context.Context, cfg servercfg.Config, dir, origin, echo string) error {
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
	rc := relay.Config{Address: ln.Addr().String(), Key: hex.EncodeToString(key[:])}
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
