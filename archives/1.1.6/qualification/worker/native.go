//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"

	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"itom.local/core/servercfg"
	"itom.local/core/socks"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
	mobile "itom.local/mobile"
)

type nativeHost struct{ ctx context.Context }

func (nativeHost) Protect(int) bool      { return true }
func (nativeHost) DNS() string           { return `["127.0.0.1"]` }
func (nativeHost) Lookup(string) string  { return `["127.0.0.1"]` }
func (nativeHost) Route(string) string   { return "VPN" }
func (h nativeHost) Startup(string) bool { return h.ctx.Err() == nil }

type nativePerformanceClient struct {
	client     *mobile.Client
	app        *stack.Stack
	fd         int
	tunBuffers [2][2]int
}

func (c *nativePerformanceClient) Close() error {
	c.app.Close()
	c.client.Close()
	unix.Close(c.fd)
	return nil
}
func (c *nativePerformanceClient) Diagnostics() string {
	var out map[string]any
	if json.Unmarshal([]byte(c.client.Diagnostics()), &out) != nil {
		return c.client.Diagnostics()
	}
	out["QualificationTUNBuffers"] = map[string]any{"requested_bytes": 4 << 20, "socket_send_receive_bytes": c.tunBuffers}
	stats := c.app.Stats()
	out["QualificationAppTCP"] = map[string]uint64{"segments_sent": stats.TCP.SegmentsSent.Value(), "segments_received": stats.TCP.ValidSegmentsReceived.Value(), "retransmits": stats.TCP.Retransmits.Value(), "timeouts": stats.TCP.Timeouts.Value(), "send_errors": stats.TCP.SegmentSendErrors.Value(), "dropped": stats.DroppedPackets.Value()}
	raw, _ := json.Marshal(out)
	return string(raw)
}
func (c *nativePerformanceClient) Stats() tunnel.Stats {
	var s tunnel.Stats
	_ = json.Unmarshal([]byte(c.client.Snapshot()), &s)
	return s
}
func (c *nativePerformanceClient) Open(ctx context.Context, target wire.Connect) (socks.Stream, netip.AddrPort, error) {
	conn, e := gonet.DialContextTCP(ctx, c.app, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 80}), Port: target.Port}, ipv4.ProtocolNumber)
	if e != nil {
		return nil, netip.AddrPort{}, e
	}
	return conn, netip.AddrPort{}, nil
}

func startPerformanceClient(ctx context.Context, p servercfg.Profile, dir, frontend string) (performanceClient, error) {
	if frontend == "direct" {
		return tunnel.StartClientWithOptions(ctx, p, dir, nil, tunnel.ClientOptions{Diagnostics: true})
	}
	if frontend != "native-tun" {
		return nil, errors.New("unsupported frontend")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	raw, e := json.Marshal(map[string]any{"Profile": p, "DNS": []string{"127.0.0.1"}, "Socks": map[string]any{"Port": port, "Authentication": false}, "Diagnostics": true})
	if e != nil {
		return nil, e
	}
	client, e := mobile.New(string(raw), dir, nativeHost{ctx})
	if e != nil {
		return nil, e
	}
	fail := func(err error) (performanceClient, error) { client.Close(); return nil, err }
	pair, e := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK, 0)
	if e != nil {
		return fail(e)
	}
	// The packet-framed TUN surrogate must not inject socketpair queue loss.
	for _, fd := range pair {
		for _, option := range []int{unix.SO_SNDBUF, unix.SO_RCVBUF} {
			if e = unix.SetsockoptInt(fd, unix.SOL_SOCKET, option, 4<<20); e != nil {
				unix.Close(pair[0])
				unix.Close(pair[1])
				return fail(e)
			}
		}
	}
	var buffers [2][2]int
	for i, fd := range pair {
		for j, option := range []int{unix.SO_SNDBUF, unix.SO_RCVBUF} {
			buffers[i][j], e = unix.GetsockoptInt(fd, unix.SOL_SOCKET, option)
			if e != nil {
				unix.Close(pair[0])
				unix.Close(pair[1])
				return fail(e)
			}
		}
	}
	if e = client.Attach(pair[0]); e != nil {
		unix.Close(pair[1])
		return fail(e)
	}
	link, e := fdbased.New(&fdbased.Options{FDs: []int{pair[1]}, MTU: 1500})
	if e != nil {
		unix.Close(pair[1])
		return fail(e)
	}
	app := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	if e := app.CreateNIC(1, link); e != nil {
		app.Close()
		unix.Close(pair[1])
		return fail(errors.New(e.String()))
	}
	if e := app.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4([4]byte{172, 19, 0, 10}), PrefixLen: 24}}, stack.AddressProperties{}); e != nil {
		app.Close()
		unix.Close(pair[1])
		return fail(errors.New(e.String()))
	}
	subnet, e2 := tcpip.NewSubnet(tcpip.AddrFrom4([4]byte{}), tcpip.MaskFromBytes([]byte{0, 0, 0, 0}))
	if e2 != nil {
		app.Close()
		unix.Close(pair[1])
		return fail(e2)
	}
	app.SetRouteTable([]tcpip.Route{{Destination: subnet, NIC: 1}})
	return &nativePerformanceClient{client, app, pair[1], buffers}, nil
}
