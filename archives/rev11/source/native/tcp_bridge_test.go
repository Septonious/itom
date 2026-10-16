package mobile

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tun "github.com/xjasonlyu/tun2socks/v2/core"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"itom.local/core/netroute"
)

type bridgeHost struct{ dnsHost }

func (bridgeHost) Route(string) string { return "DIRECT" }

// Use the actual Android gonet/tun2socks TCP bridge, not net.Pipe as a substitute
// for local TCP. The upstream is direct loopback: this is a bridge/half-close
// integrity check, independently of the IMAP speed model.
func TestNativeTCPBridgeConcurrentHalfCloseAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	left, right := channel.New(1024, 1500, ""), channel.New(1024, 1500, "")
	app := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	defer app.Close()
	if err := app.CreateNIC(1, left); err != nil {
		t.Fatal(err)
	}
	if err := app.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddrFrom4([4]byte{172, 19, 0, 1}).WithPrefix()}, stack.AddressProperties{}); err != nil {
		t.Fatal(err)
	}
	app.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: 1}})
	db, err := openDNS(filepath.Join(t.TempDir(), "bridge-dns.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	destination, err := db.address("bridge.example")
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{ctx: ctx, cancel: cancel, dns: db, host: bridgeHost{}, slots: make(chan struct{}, 128), dialer: &netroute.Dialer{Lookup: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}}}
	bridge, err := tun.CreateStack(&tun.Config{LinkEndpoint: right, TransportHandler: nativeHandler{c}})
	if err != nil {
		t.Fatal(err)
	}
	c.stack = bridge
	var packets sync.WaitGroup
	for _, pair := range [][2]*channel.Endpoint{{left, right}, {right, left}} {
		packets.Add(1)
		go func(a, b *channel.Endpoint) {
			defer packets.Done()
			for {
				p := a.ReadContext(ctx)
				if p == nil {
					return
				}
				inbound := p.CloneToInbound()
				b.InjectInbound(ipv4.ProtocolNumber, inbound)
				inbound.DecRef()
				p.DecRef()
			}
		}(pair[0], pair[1])
	}
	defer func() { c.Close(); left.Close(); right.Close(); packets.Wait() }()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stopAccept := context.AfterFunc(ctx, func() { listener.Close() })
	defer stopAccept()
	const count = 6
	request := bytes.Repeat([]byte{'q'}, 80)
	response := bytes.Repeat([]byte("verified-local-native-tcp"), 8192)
	errors := make(chan error, count*2)
	var targets, apps sync.WaitGroup
	targets.Add(count)
	go func() {
		for i := 0; i < count; i++ {
			sock, err := listener.Accept()
			if err != nil {
				errors <- err
				for j := i; j < count; j++ {
					targets.Done()
				}
				return
			}
			go func() {
				defer targets.Done()
				defer sock.Close()
				sock.SetDeadline(time.Now().Add(5 * time.Second))
				got, e := io.ReadAll(sock)
				if e != nil || !bytes.Equal(got, request) {
					errors <- fmt.Errorf("target request/half-close %v bytes%d", e, len(got))
					return
				}
				if _, e = sock.Write(response); e != nil {
					errors <- e
				}
			}()
		}
	}()
	for i := 0; i < count; i++ {
		apps.Add(1)
		go func() {
			defer apps.Done()
			conn, e := gonet.DialContextTCP(ctx, app, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(destination.As4()), Port: uint16(listener.Addr().(*net.TCPAddr).Port)}, ipv4.ProtocolNumber)
			if e != nil {
				errors <- e
				return
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			if _, e = conn.Write(request); e != nil {
				errors <- e
				return
			}
			if e = conn.CloseWrite(); e != nil {
				errors <- e
				return
			}
			got, e := io.ReadAll(conn)
			if e != nil || !bytes.Equal(got, response) {
				errors <- fmt.Errorf("native download after upload EOF %v bytes%d", e, len(got))
			}
		}()
	}
	apps.Wait()
	targets.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if c.up.Load() != uint64(count*len(request)) || c.down.Load() != uint64(count*len(response)) {
		t.Fatal("native copy counters", c.up.Load(), c.down.Load())
	}
	// Cancel while a seventh real bridge connection is still reading in both
	// directions, rather than checking only already-completed workers.
	activeListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer activeListener.Close()
	stopActiveAccept := context.AfterFunc(ctx, func() { activeListener.Close() })
	defer stopActiveAccept()
	activeReady := make(chan error, 1)
	go func() {
		sock, err := activeListener.Accept()
		if err != nil {
			activeReady <- err
			return
		}
		defer sock.Close()
		var b [1]byte
		_, err = io.ReadFull(sock, b[:])
		activeReady <- err
		<-ctx.Done()
	}()
	active, err := gonet.DialContextTCP(ctx, app, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(destination.As4()), Port: uint16(activeListener.Addr().(*net.TCPAddr).Port)}, ipv4.ProtocolNumber)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if _, err = active.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-activeReady:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("active bridge did not start")
	}
	cancel()
	joined := make(chan struct{})
	go func() { c.wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("native bridge worker leaked after cancellation")
	}
	if c.tcp.Load() != 0 {
		t.Fatal("cancelled TCP bridge retained active stream", c.tcp.Load())
	}
	t.Log("actual_gonet_tcp_connections=7 upload_half_close=true download_integrity=true active_cancellation=true; bridge check, not IMAP bandwidth qualification")
}
