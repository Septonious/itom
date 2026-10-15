package datapath

import (
	"context"
	"itom.local/core/wire"
	"net"
	"net/netip"
	"testing"
)

type mappedResolver struct{ ip netip.Addr }

func (r mappedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{r.ip}, nil
}

func TestExitResolverMappedIPv4DialsAndPreservesPolicy(t *testing.T) {
	for _, host := range []string{"localhost", "mapped.example"} {
		t.Run(host, func(t *testing.T) {
			x, listener, _ := tcpFixture(t, true)
			if host == "mapped.example" {
				x.cfg.Resolver = mappedResolver{netip.MustParseAddr("::ffff:127.0.0.1")}
			}
			f := control(t, 1, wire.Connect{Host: host, Port: uint16(listener.Addr().(*net.TCPAddr).Port)})
			action, e := x.Request(0, f)
			if e != nil {
				t.Fatal(e)
			}
			result := x.Dial(*action.Dial)
			if result.conn == nil || result.reason != wire.ReasonNone {
				t.Fatal("resolver IPv4 representation rejected", result.reason, result.diagnostic)
			}
			result.conn.Close()
		})
	}
	for _, addr := range []string{"::ffff:127.0.0.1", "::ffff:192.168.1.1", "::ffff:169.254.169.254", "::ffff:198.18.0.1", "::ffff:0.0.0.0", "::ffff:224.0.0.1"} {
		t.Run(addr, func(t *testing.T) {
			x, _, _ := tcpFixture(t, false)
			x.cfg.Resolver = mappedResolver{netip.MustParseAddr(addr)}
			action, e := x.Request(0, control(t, 1, wire.Connect{Host: "mapped.example", Port: 443}))
			if e != nil {
				t.Fatal(e)
			}
			result := x.Dial(*action.Dial)
			if result.conn != nil || result.reason != wire.ReasonPolicy {
				t.Fatal("mapped address bypassed policy", addr, result.reason)
			}
		})
	}
	for _, addr := range []string{"::ffff:1.1.1.1", "::ffff:8.8.8.8"} {
		ip := netip.MustParseAddr(addr).Unmap()
		if !allowedIP(ip, false) {
			t.Fatal("public mapped DNS record rejected after normalization", addr)
		}
	}
}
