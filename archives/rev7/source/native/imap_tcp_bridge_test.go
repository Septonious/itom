//go:build itomtest

package mobile

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	tun "github.com/xjasonlyu/tun2socks/v2/core"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
)

type imapBridgeHost struct{ dnsHost }

func (imapBridgeHost) Route(string) string { return "VPN" }

type bridgeProviderSession struct {
	imapserver.Session
	appends, fetches *atomic.Int64
}

func (s *bridgeProviderSession) Append(folder string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	s.appends.Add(1)
	time.Sleep(10 * time.Millisecond)
	return s.Session.Append(folder, r, o)
}
func (s *bridgeProviderSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	s.fetches.Add(1)
	time.Sleep(10 * time.Millisecond)
	return s.Session.Fetch(w, set, o)
}

// End-to-end bridge workload: TCP packets -> native gonet -> logical streams ->
// Noise + MIME -> TLS IMAP -> production server -> real TCP exit. Runs cold
// single, cold eight-stream, and a second consecutive eight-stream burst, using
// 4 KB app writes. This is an integrity/startup regression, not a 200 Mbps
// certification: no provider link-capacity/mobile radio model is applied here.
func TestNativeIMAPTCPBridgeSingleMultiConsecutive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	cfg, e := servercfg.New("native-bridge")
	if e != nil {
		t.Fatal(e)
	}
	u, e := cfg.AddUser("native")
	if e != nil {
		t.Fatal(e)
	}
	cfg.DiagnosticsMode = "off"
	roots := x509.NewCertPool()
	ts := httptest.NewTLSServer(nil)
	cert := ts.TLS.Certificates[0]
	roots.AddCert(ts.Certificate())
	ts.Close()
	var appends, fetches atomic.Int64
	var providers []*imapserver.Server
	defer func() {
		for _, s := range providers {
			s.Close()
		}
	}()
	for i := 1; i <= 4; i++ {
		mem := imapmemserver.New()
		username := fmt.Sprintf("test-%d", i)
		mem.AddUser(imapmemserver.NewUser(username, "test-secret"))
		srv := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &bridgeProviderSession{Session: mem.NewSession(), appends: &appends, fetches: &fetches}, nil, nil
		}, Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapIdle: {}}, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}})
		ln, e := net.Listen("tcp4", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { ln.Close() })
		providers = append(providers, srv)
		go srv.Serve(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
		cfg.Accounts = append(cfg.Accounts, servercfg.Account{ID: fmt.Sprintf("a%02d", i), Mail: mailbox.Config{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: username, Password: "test-secret"}})
	}
	dial := func(ctx context.Context, c mailbox.Config, folders []string) (*mailbox.Client, error) {
		return mailbox.DialWithRoots(ctx, c, folders, roots)
	}
	serverDir := filepath.Join(t.TempDir(), "server")
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tunnel.ServeIntegrationFixture(ctx, cfg, serverDir, dial, nil)
	}()
	pool, e := tunnel.StartClientWithOptions(ctx, servercfg.Profile{Schema: 1, Server: cfg.Server, Provider: cfg.Provider, User: u, Accounts: cfg.Accounts}, filepath.Join(t.TempDir(), "client"), dial, tunnel.ClientOptions{})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		cancel()
		pool.Close()
		select {
		case e := <-serverDone:
			if e != nil && e != context.Canceled {
				t.Logf("server shutdown: %v", e)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not join")
		}
	}()
	readyDeadline := time.Now().Add(30 * time.Second)
	for pool.Snapshot().ActiveLines < 8 {
		if time.Now().After(readyDeadline) {
			t.Fatal("four carriers did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
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
	db, e := openDNS(filepath.Join(t.TempDir(), "dns.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	dest, e := db.address("localhost")
	if e != nil {
		t.Fatal(e)
	}
	c := &Client{ctx: ctx, cancel: cancel, pool: pool, dns: db, host: imapBridgeHost{}, slots: make(chan struct{}, 128)}
	bridge, e := tun.CreateStack(&tun.Config{LinkEndpoint: right, TransportHandler: nativeHandler{c}})
	if e != nil {
		t.Fatal(e)
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
				in := p.CloneToInbound()
				b.InjectInbound(ipv4.ProtocolNumber, in)
				in.DecRef()
				p.DecRef()
			}
		}(pair[0], pair[1])
	}
	defer func() { cancel(); c.Close(); left.Close(); right.Close(); packets.Wait() }()
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	request := bytes.Repeat([]byte("native-upload-integrity"), 16384)
	response := bytes.Repeat([]byte("native-download-integrity"), 16384)
	targetErrors := make(chan error, 32)
	var targetCount atomic.Int64
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			targetCount.Add(1)
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				got := make([]byte, len(request))
				if _, e := io.ReadFull(conn, got); e != nil {
					targetErrors <- e
					return
				}
				if !bytes.Equal(got, request) {
					targetErrors <- fmt.Errorf("exit integrity")
					return
				}
				for off := 0; off < len(response); off += 4096 {
					end := min(off+4096, len(response))
					if _, e := conn.Write(response[off:end]); e != nil {
						targetErrors <- e
						return
					}
				}
				if _, e := io.ReadAll(conn); e != nil {
					targetErrors <- e
				}
			}()
		}
	}()
	for phase, n := range []int{1, 8, 8} {
		start := time.Now()
		beforeAppend, beforeFetch := appends.Load(), fetches.Load()
		var wg sync.WaitGroup
		errs := make(chan error, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				conn, e := gonet.DialContextTCP(ctx, app, tcpip.FullAddress{NIC: 1, Addr: tcpip.AddrFrom4(dest.As4()), Port: uint16(listener.Addr().(*net.TCPAddr).Port)}, ipv4.ProtocolNumber)
				if e != nil {
					errs <- e
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				for off := 0; off < len(request); off += 4096 {
					end := min(off+4096, len(request))
					if _, e = conn.Write(request[off:end]); e != nil {
						errs <- e
						return
					}
				}
				if e = conn.CloseWrite(); e != nil {
					errs <- e
					return
				}
				got, e := io.ReadAll(conn)
				if e != nil || !bytes.Equal(got, response) {
					errs <- fmt.Errorf("bridge response integrity bytes=%d err=%v", len(got), e)
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Logf("client: %+v", pool.Snapshot())
			t.Fatal(e)
		}
		t.Logf("native_imap_phase=%d streams=%d elapsed_ms=%d upload_bytes=%d download_bytes=%d append_commands=%d fetch_commands=%d", phase+1, n, time.Since(start).Milliseconds(), n*len(request), n*len(response), appends.Load()-beforeAppend, fetches.Load()-beforeFetch)
	}
	if targetCount.Load() != 17 {
		t.Fatal("Multi did not open independent exits", targetCount.Load())
	}
	select {
	case e := <-targetErrors:
		t.Fatal(e)
	default:
	}
	if c.up.Load() < uint64(17*len(request)) || c.down.Load() < uint64(17*len(response)) {
		t.Fatal("native byte counters", c.up.Load(), c.down.Load())
	}
}
