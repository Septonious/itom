//go:build linux

package mobile

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/link/fdbased"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

type trafficHost struct{}

func (trafficHost) Protect(int) bool     { return true }
func (trafficHost) DNS() string          { return `["127.0.0.1"]` }
func (trafficHost) Lookup(string) string { return `["127.0.0.1"]` }
func (trafficHost) Route(string) string  { return "VPN" }
func (trafficHost) Startup(string) bool  { return true }

// Launched by the core's real LOGIN/SELECT/standing-server fixture. No native
// transport, crypto, grant, exit bridge or application copy path is substituted.
// The datagram pair carries raw IPv4 packets, as an unprivileged TUN surrogate.
func TestRealNativeClientProcessHelper(t *testing.T) {
	input := os.Getenv("ITOM_NATIVE_TEST_CONFIG")
	if input == "" {
		return
	}
	raw, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(string(raw), os.Getenv("ITOM_NATIVE_TEST_STATE"), trafficHost{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		if t.Failed() {
			_ = os.WriteFile(os.Getenv("ITOM_NATIVE_TEST_EVIDENCE"), []byte(c.Diagnostics()), 0600)
		}
	}()
	roots := x509.NewCertPool()
	pem, err := os.ReadFile(os.Getenv("ITOM_NATIVE_TEST_HTTPS_ROOT"))
	if err != nil || !roots.AppendCertsFromPEM(pem) {
		t.Fatal("HTTPS trust", err)
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Attach(pair[0]); err != nil {
		unix.Close(pair[1])
		t.Fatal(err)
	}
	defer unix.Close(pair[1])
	link, err := fdbased.New(&fdbased.Options{FDs: []int{pair[1]}, MTU: 1500})
	if err != nil {
		t.Fatal(err)
	}
	app := stack.New(stack.Options{NetworkProtocols: []stack.NetworkProtocolFactory{ipv4.NewProtocol}, TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol}})
	defer app.Close()
	if e := app.CreateNIC(1, link); e != nil {
		t.Fatal(e)
	}
	if e := app.AddProtocolAddress(1, tcpip.ProtocolAddress{Protocol: ipv4.ProtocolNumber, AddressWithPrefix: tcpip.AddressWithPrefix{Address: tcpip.AddrFrom4([4]byte{172, 19, 0, 10}), PrefixLen: 24}}, stack.AddressProperties{}); e != nil {
		t.Fatal(e)
	}
	app.SetRouteTable([]tcpip.Route{{Destination: headerIPv4EmptySubnet(), NIC: 1}})
	socks, err := proxy.SOCKS5("tcp", c.listener.Addr().String(), nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"socks", "tun"} {
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond}
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			if mode == "socks" {
				return socks.(proxy.ContextDialer).DialContext(ctx, "tcp", "203.0.113.80:443")
			}
			return gonet.DialContextTCP(ctx, app, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4([4]byte{203, 0, 113, 80}), Port: 443}, ipv4.ProtocolNumber)
		}
		hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		for _, size := range []int{4096, 128 << 10} {
			payload := bytes.Repeat([]byte{37}, size)
			resp, e := hc.Post("https://example.com/", "application/octet-stream", bytes.NewReader(payload))
			if e != nil {
				t.Fatalf("%s size=%d HTTP: %v diagnostics=%s", mode, size, e, c.Diagnostics())
			}
			got, e := io.ReadAll(resp.Body)
			resp.Body.Close()
			if e != nil || !bytes.Equal(got, payload) {
				t.Fatalf("%s size=%d body=%d err=%v diagnostics=%s", mode, size, len(got), e, c.Diagnostics())
			}
		}
		tr.CloseIdleConnections()
	}
	var stats map[string]any
	if err = json.Unmarshal([]byte(c.Snapshot()), &stats); err != nil {
		t.Fatal(err)
	}
	if stats["Upload"].(float64) < 2*(4096+128<<10) || stats["Download"].(float64) < 2*(4096+128<<10) {
		t.Fatal("native application counters", stats)
	}
	if p := c.pool.Probe(); p.Samples == 0 || p.RTTMillis <= 0 {
		t.Fatalf("real peer echo missing: %+v", p)
	}
	if evidence := c.Diagnostics(); !strings.Contains(evidence, `"Flows":`) || !strings.Contains(evidence, `"Native":`) {
		t.Fatal("runtime/bridge evidence missing", evidence)
	}
	trace, e := os.ReadFile(filepath.Join(os.Getenv("ITOM_NATIVE_TEST_STATE"), "transport", "transport-trace.jsonl"))
	if e != nil || !bytes.Contains(trace, []byte(`"client_runtime"`)) {
		t.Fatal("running client omitted runtime trace", e)
	}
	if err = os.WriteFile(os.Getenv("ITOM_NATIVE_TEST_STOP"), []byte("intentional fixture owner shutdown"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("actual native SOCKS and IPv4 TUN HTTPS upload/download integrity passed")
}

func headerIPv4EmptySubnet() tcpip.Subnet {
	s, e := tcpip.NewSubnet(tcpip.AddrFrom4([4]byte{}), tcpip.MaskFromBytes([]byte{0, 0, 0, 0}))
	if e != nil {
		panic(e)
	}
	return s
}
