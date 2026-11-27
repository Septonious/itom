package mobile

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"itom.local/core/wire"
)

// ExitIdentity is one bounded HTTPS request over the authenticated transport,
// not Android's network resolver, a routing-rule verdict, or a direct socket.
// It follows the configured server-side exit and cannot fall back to direct.
func (c *Client) ExitIdentity() (string, error) {
	const host = "www.cloudflare.com"
	pool := c.currentPool()
	if pool == nil || c.rebinding.Load() {
		return "", errors.New("EXIT_NOT_CONNECTED")
	}
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != host+":443" {
			return nil, errors.New("EXIT_TARGET_INVALID")
		}
		stream, _, err := pool.Open(ctx, wire.Connect{Host: host, Port: 443})
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
	if c.currentPool() != pool || c.rebinding.Load() || ctx.Err() != nil {
		return "", errors.New("EXIT_GENERATION_CHANGED")
	}
	data, err := json.Marshal(result)
	return string(data), err
}
