package mailstream

import (
	"context"
	"net"
	"net/netip"
	"strconv"
)

// Numeric dialing uses the addresses actually checked here, so a second DNS
// lookup cannot rebind a public hostname to a local management endpoint.
func publicDial(ctx context.Context, network, target string) (net.Conn, error) {
	if network != "tcp" {
		return nil, ErrProtocol
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, ErrProtocol
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, ErrProtocol
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, ErrClosed
	}
	allowed := false
	for _, address := range addresses {
		address = address.Unmap()
		if !publicAddress(address) {
			continue
		}
		allowed = true
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(address.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	if !allowed {
		return nil, ErrAuth
	}
	return nil, ErrClosed
}
func publicAddress(address netip.Addr) bool {
	return address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast() && !address.IsUnspecified()
}
