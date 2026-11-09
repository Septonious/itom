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
        for _, allowed := range []struct{ virtual, actual string }{{"203.0.113.80",origin},{"203.0.113.80",echo}} {
            _,port,_:=net.SplitHostPort(allowed.actual)
            if target.Host==allowed.virtual && strconv.Itoa(int(target.Port))==port { address=allowed.actual }
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
