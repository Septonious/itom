package mobile

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"itom.local/core/wire"
)

// ExitIdentity is one bounded HTTPS request over the authenticated transport,
// not Android's network resolver, a routing-rule verdict, or a direct socket.
// It follows the configured server-side exit and cannot fall back to direct.
func (c *Client) ExitIdentity() (string, error) {
	pool := c.currentPool()
	if pool == nil || c.rebinding.Load() {
		return "", errors.New("EXIT_NOT_CONNECTED")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 18*time.Second)
	defer cancel()
	// Both targets are public Cloudflare resolver/HTTP trace endpoints. TLS
	// still verifies one.one.one.one, and the dial always goes through pool.Open.
	for _, target := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		attempt, stop := context.WithTimeout(ctx, 8*time.Second)
		result, err := c.exitIdentityFamily(attempt, pool, target)
		stop()
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return "", errors.New("EXIT_IP_UNAVAILABLE")
}
func (c *Client) exitIdentityFamily(parent context.Context, pool transportPool, target string) (string, error) {
	const host = "one.one.one.one"
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != host+":443" {
			return nil, errors.New("EXIT_TARGET_INVALID")
		}
		stream, _, err := pool.Open(ctx, wire.Connect{Host: target, Port: 443})
		if err != nil {
			return nil, err
		}
		conn, ok := stream.(net.Conn)
		if !ok {
			stream.Close()
			return nil, errors.New("EXIT_STREAM_INVALID")
		}
		return conn, nil
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/cdn-cgi/trace", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ITOM exit identity")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New("EXIT_HTTP_STATUS")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errors.New("EXIT_RESPONSE_INVALID")
	}
	result, err := parseExitTrace(string(raw))
	if err != nil {
		return "", err
	}
	ip, parseErr := netip.ParseAddr(result.IP)
	if parseErr != nil || ip.Unmap().Is4() != netip.MustParseAddr(target).Is4() {
		return "", errors.New("EXIT_IP_FAMILY")
	}
	if c.currentPool() != pool || c.rebinding.Load() || ctx.Err() != nil {
		return "", errors.New("EXIT_GENERATION_CHANGED")
	}
	data, err := json.Marshal(result)
	return string(data), err
}
