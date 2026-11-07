package main

import (
	"context"
	"errors"
	"net"
	"strconv"

	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

func serveFixture(ctx context.Context, cfg servercfg.Config, dir, origin, echo string) error {
	return tunnel.ServeManaged(ctx, cfg, dir, nil, func(call context.Context, _ servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
		address := net.JoinHostPort(target.Host, strconv.Itoa(int(target.Port)))
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
