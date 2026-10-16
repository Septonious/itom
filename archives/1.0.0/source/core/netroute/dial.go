// Package netroute connects through a platform resolver without consulting DNS
// inside the VPN. TCP retains protected socket creation and dual-stack racing.
package netroute

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

type Dialer struct {
	Socket net.Dialer
	Lookup func(context.Context, string) ([]netip.Addr, error)
}

var ErrProtect = errors.New("VPN_PROTECT_FAILED")

func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d.Socket.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Socket.Timeout)
		defer cancel()
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return d.Socket.DialContext(ctx, network, address)
	}
	if d.Lookup == nil {
		return nil, &net.DNSError{Err: "platform resolver missing", Name: host}
	}
	addresses, err := d.Lookup(ctx, host)
	if err != nil {
		return nil, &net.DNSError{Err: "platform lookup failed", Name: host, IsTimeout: errors.Is(err, context.DeadlineExceeded), IsTemporary: errors.Is(err, context.Canceled)}
	}
	var primary, fallback []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, ip := range addresses {
		ip = ip.Unmap()
		if !ip.IsValid() || seen[ip] || network == "tcp4" && !ip.Is4() || network == "tcp6" && !ip.Is6() {
			continue
		}
		seen[ip] = true
		if len(primary) == 0 || primary[0].Is4() == ip.Is4() {
			primary = append(primary, ip)
		} else {
			fallback = append(fallback, ip)
		}
	}
	if len(primary) == 0 {
		return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
	}
	if d.Socket.FallbackDelay < 0 {
		primary = append(primary, fallback...)
		fallback = nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result)
	run := func(ips []netip.Addr) {
		var r result
		for _, ip := range ips {
			r.conn, r.err = d.Socket.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if r.err == nil || ctx.Err() != nil {
				break
			}
		}
		select {
		case results <- r:
		case <-ctx.Done():
			if r.conn != nil {
				_ = r.conn.Close()
			}
		}
	}
	go run(primary)
	delay := d.Socket.FallbackDelay
	if delay == 0 {
		delay = 300 * time.Millisecond
	}
	if delay < 0 {
		delay = 300 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	fallbackStarted := false
	pending := 1
	var firstErr error
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if len(fallback) > 0 && !fallbackStarted {
				fallbackStarted = true
				pending++
				go run(fallback)
			}
		case r := <-results:
			pending--
			if r.err == nil {
				return r.conn, nil
			}
			if firstErr == nil {
				firstErr = r.err
			}
			if len(fallback) > 0 && !fallbackStarted {
				fallbackStarted = true
				pending++
				go run(fallback)
			}
			if pending == 0 {
				return nil, firstErr
			}
		}
	}
}
