package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRealTLSIMAPNoiseRoundTripTwoUsers(t *testing.T) {
	ts := httptest.NewTLSServer(nil)
	cert := ts.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	ts.Close()
	previous := dialIMAP
	dialIMAP = func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
		return mailbox.DialWithRoots(ctx, cfg, folders, roots)
	}
	defer func() { dialIMAP = previous }()

	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("user", "secret"))
	srv := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return mem.NewSession(), nil, nil
	}, Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapUIDPlus: {}}})
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	go srv.Serve(tlsLn)
	defer srv.Close()
	defer tlsLn.Close()
	cfg, e := servercfg.New("test")
	if e != nil {
		t.Fatal(e)
	}
	cfg.Accounts = []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: "user", Password: "secret"}}}
	if _, e = cfg.AddUser("first"); e != nil {
		t.Fatal(e)
	}
	if _, e = cfg.AddUser("second"); e != nil {
		t.Fatal(e)
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, cfg, t.TempDir(), func(s string) { t.Log(s) }) }()
	for _, u := range cfg.Users {
		p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: cfg.Server, Provider: cfg.Provider, User: u, Accounts: cfg.Accounts}
		rtt, e := RunClient(ctx, p, "a01", t.TempDir())
		if e != nil || rtt <= 0 {
			t.Fatal(rtt, e)
		}
	}
	stop()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}
func TestProbeRejectsMalformedAndWrongBinding(t *testing.T) {
	if _, e := parse([]byte("{}")); e == nil {
		t.Fatal("empty envelope")
	}
	if _, e := parse(make([]byte, 17000)); e == nil {
		t.Fatal("unbounded envelope")
	}
	c, e := servercfg.New("test")
	if e != nil {
		t.Fatal(e)
	}
	u, e := c.AddUser("first")
	if e != nil {
		t.Fatal(e)
	}
	b := binding(c.Server, u, "a01")
	b.Session[0] = 1
	b.Handshake[0] = 1
	if !checkBinding(b, c.Server, u, "a01") || checkBinding(b, c.Server, u, "a02") {
		t.Fatal("binding mismatch")
	}
}
