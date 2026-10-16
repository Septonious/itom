package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
)

type checkSession struct {
	imapserver.Session
	mutations *atomic.Int32
}

func (s *checkSession) Select(string, *imap.SelectOptions) (*imap.SelectData, error) {
	s.mutations.Add(1)
	return nil, errors.New("unexpected SELECT")
}
func (s *checkSession) Create(string, *imap.CreateOptions) error {
	s.mutations.Add(1)
	return errors.New("unexpected CREATE")
}
func (s *checkSession) Append(string, imap.LiteralReader, *imap.AppendOptions) (*imap.AppendData, error) {
	s.mutations.Add(1)
	return nil, errors.New("unexpected APPEND")
}

func TestIMAPCheckAuthenticatesOneSocketWithoutTransportOrMutation(t *testing.T) {
	ts := httptest.NewTLSServer(nil)
	cert := ts.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(ts.Certificate())
	ts.Close()
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("user", "secret"))
	var sessions, mutations atomic.Int32
	srv := imapserver.New(&imapserver.Options{NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		sessions.Add(1)
		return &checkSession{Session: mem.NewSession(), mutations: &mutations}, nil, nil
	}, Caps: imap.CapSet{imap.CapIMAP4rev1: {}}})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	go srv.Serve(tlsLn)
	defer srv.Close()
	defer tlsLn.Close()
	cfg, err := servercfg.New("test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, TLS: "tls", Username: "user", Password: "secret"}}}
	if _, err = cfg.AddUser("test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
		return mailbox.DialWithRoots(ctx, cfg, folders, roots)
	}
	var out bytes.Buffer
	if err := checkIMAP(ctx, cfg, "a01", &out, dial); err != nil {
		t.Fatal(err, out.String())
	}
	if !strings.Contains(out.String(), "outcome=ok stage=noop") || sessions.Load() != 1 || mutations.Load() != 0 {
		t.Fatalf("out=%q sessions=%d mutations=%d", out.String(), sessions.Load(), mutations.Load())
	}
	out.Reset()
	cfg.Accounts[0].Mail.Password = "wrong-secret"
	if err := checkIMAP(ctx, cfg, "a01", &out, dial); err == nil || !strings.Contains(out.String(), "stage=login reason=rejected") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("password leaked")
	}
	called := false
	noDial := func(context.Context, mailbox.Config, []string) (*mailbox.Client, error) {
		called = true
		return nil, errors.New("secret.example password=secret")
	}
	for _, id := range []string{"missing", "a01"} {
		if id == "a01" {
			cfg.Users[0].Enabled = false
		}
		if err := checkIMAP(ctx, cfg, id, &out, noDial); err == nil || called {
			t.Fatal("invalid check attempted dial", err)
		}
	}
}
