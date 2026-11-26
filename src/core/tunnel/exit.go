package tunnel

import (
	"context"
	"itom.local/core/relay"
	"itom.local/mailstream"
	"net"
)

func dialUserExit(ctx context.Context, network, address string, upstream *relay.Dialer) (net.Conn, error) {
	if network != "tcp" {
		return nil, mailstream.ErrProtocol
	}
	direct := func(ctx context.Context, target string) (net.Conn, error) {
		return mailstream.PublicDial(ctx, network, target)
	}
	if upstream == nil {
		return direct(ctx, address)
	}
	return upstream.Dial(ctx, address, direct)
}
