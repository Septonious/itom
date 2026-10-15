//go:build itomtest

package mobile

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"itom.local/core/adminapi"
	"itom.local/core/adminserver"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

func TestNativeAdminIMAPAuthorityAndHeartbeatSurviveReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	cfg, e := servercfg.New("admin-imap-test")
	if e != nil {
		t.Fatal(e)
	}
	u, e := cfg.AddUser("admin")
	if e != nil {
		t.Fatal(e)
	}
	cfg.DiagnosticsMode = "off"
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	for i := 1; i <= 2; i++ {
		memory := imapmemserver.New()
		username := fmt.Sprintf("admin-test-%d", i)
		memory.AddUser(imapmemserver.NewUser(username, "test-only-password"))
		srv := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return memory.NewSession(), nil, nil
		}, Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}, imap.CapIdle: {}}, TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}})
		ln, e := net.Listen("tcp4", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { ln.Close(); srv.Close() })
		go srv.Serve(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
		cfg.Accounts = append(cfg.Accounts, servercfg.Account{ID: fmt.Sprintf("a%02d", i), Mail: mailbox.Config{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: username, Password: "test-only-password"}})
	}
	state := t.TempDir()
	db := filepath.Join(state, "server.db")
	if e = servercfg.Update(db, func(c *servercfg.Config) error { *c = cfg; return nil }); e != nil {
		t.Fatal(e)
	}
	management := adminserver.New(ctx, state)
	defer management.Close()
	management.Launch = func(id string) error {
		g, e := servercfg.GuardStatus(db)
		if e != nil {
			return e
		}
		q, e := adminapi.DecodeRequest(g.Plan)
		if e != nil {
			return e
		}
		if e = servercfg.ApplyGuard(db, id, time.Now(), func(c *servercfg.Config) error { return c.SetNotice(q.Text) }); e != nil {
			return e
		}
		return servercfg.CompleteGuardAction(db, id, time.Now())
	}
	dial := func(ctx context.Context, c mailbox.Config, folders []string) (*mailbox.Client, error) {
		return mailbox.DialWithRoots(ctx, c, folders, roots)
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- tunnel.ServeManagedIntegrationFixture(ctx, cfg, filepath.Join(state, "tunnel-server"), dial, nil, management.Dial)
	}()
	defer func() {
		cancel()
		select {
		case <-serverDone:
		case <-time.After(5 * time.Second):
			t.Error("managed server did not stop")
		}
	}()
	clientDir := filepath.Join(t.TempDir(), "client")
	start := func() *Client {
		cctx, ccancel := context.WithCancel(ctx)
		p, e := tunnel.StartClientWithOptions(cctx, servercfg.Profile{Schema: 1, Server: cfg.Server, Provider: cfg.Provider, User: u, Accounts: cfg.Accounts}, clientDir, dial, tunnel.ClientOptions{})
		if e != nil {
			t.Fatal(e)
		}
		c := &Client{ctx: cctx, cancel: ccancel, pool: p}
		c.startAdmin(u.Key, u.ID)
		until := time.Now().Add(15 * time.Second)
		for {
			var v adminView
			_ = json.Unmarshal([]byte(c.AdminSnapshot()), &v)
			if v.Authorized {
				break
			}
			if time.Now().After(until) {
				c.Close()
				t.Fatal("server did not authorize native admin over IMAP")
			}
			time.Sleep(25 * time.Millisecond)
		}
		return c
	}
	client := start()
	defer func() { client.Close() }()
	raw, e := client.AdminCall(`{"operation":"status"}`)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(raw, u.Key) || strings.Contains(raw, "test-only-password") {
		t.Fatal("native admin snapshot exposed credentials")
	}
	// Another application routed through exactly the same authenticated VPN can
	// reach this stream but cannot sign a command using the app's private key.
	stream, _, e := client.pool.Open(ctx, wire.Connect{Host: adminapi.Host, Port: adminapi.Port})
	if e != nil {
		t.Fatal(e)
	}
	q, _ := json.Marshal(adminapi.Request{Operation: "status"})
	denied, e := adminapi.Call(ctx, stream.(net.Conn), q, strings.Repeat("0", 64))
	if e != nil || denied.OK || denied.Error != "ADMIN_ACCESS_DENIED" {
		t.Fatal("VPN application gained admin authority", denied, e)
	}
	id, e := adminapi.NewID()
	if e != nil {
		t.Fatal(e)
	}
	command, _ := json.Marshal(adminapi.Request{Operation: "global_message", ID: id, Text: "&cAdmin &aIMAP test"})
	if raw, e = client.AdminCall(string(command)); e != nil {
		t.Fatal(e)
	}
	var reply adminapi.Response
	if json.Unmarshal([]byte(raw), &reply) != nil || !reply.OK {
		t.Fatal(raw)
	}
	waitBeat := func(after int64) servercfg.Guard {
		until := time.Now().Add(14 * time.Second)
		for {
			g, e := servercfg.GuardStatus(db)
			if e != nil {
				t.Fatal(e)
			}
			if g.LastBeatMS > after {
				return g
			}
			if time.Now().After(until) {
				t.Fatal("native background heartbeat did not prove two-way IMAP recovery")
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	g, _ := servercfg.GuardStatus(db)
	g = waitBeat(g.AppliedMS)
	old := g.LastBeatMS
	client.Close()
	client = start()
	g = waitBeat(old)
	if g.ID != id || g.State != "observing" {
		t.Fatal("reconnection replaced or prematurely committed trial", g.ID, g.State)
	}
	current, e := servercfg.Load(db)
	if e != nil || current.Notice.Text != "&cAdmin &aIMAP test" {
		t.Fatal("IMAP admin command was not applied", e)
	}
}
