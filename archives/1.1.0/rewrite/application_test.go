package mailstream

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// This is one whole-path acceptance scenario, not a controller throughput
// formula or substituted grant fixture: verified TLS IMAP, fresh Noise startup,
// real TCP, real HTTPS, a body larger than stream credit, then user restarts.
// It intentionally provides no UIDPLUS and permits no cleanup commands. It
// cannot establish real-provider performance or sustained reclamation.
func TestApplicationHTTPSAndUserRestartsWithoutUIDPlusOrCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	accounts, cleanupCalls := applicationMailServer(t)
	serverLink, err := NewIMAPLink(ctx, "application", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer serverLink.Close()
	clientLink, err := NewIMAPLink(ctx, "application", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer clientLink.Close()
	psk := make([]byte, 32)
	if _, err = rand.Read(psk); err != nil {
		t.Fatal(err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- Serve(ctx, serverLink, "application", psk, nil, nil) }()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(w, r.Body)
	}))
	defer origin.Close()
	payload := make([]byte, streamWindow+128<<10)
	if _, err = rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		start := time.Now()
		startup, stop := context.WithTimeout(ctx, 10*time.Second)
		session, err := Connect(startup, clientLink, "application", psk)
		stop()
		if err != nil {
			t.Fatalf("restart %d: %v client=%+v server=%+v", round, err, clientLink.Snapshot(), serverLink.Snapshot())
		}
		startupMS := time.Since(start).Milliseconds()
		baseTransport := origin.Client().Transport.(*http.Transport).Clone()
		baseTransport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
		baseTransport.ForceAttemptHTTP2 = false
		client := &http.Client{Transport: baseTransport, Timeout: 15 * time.Second}
		response, err := client.Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			session.Close()
			t.Fatalf("HTTPS restart %d: %v client=%+v mail=%+v", round, err, session.Snapshot(), clientLink.Snapshot())
		}
		got, err := io.ReadAll(io.LimitReader(response.Body, int64(len(payload)+1)))
		response.Body.Close()
		if err != nil || !bytes.Equal(got, payload) {
			session.Close()
			t.Fatalf("HTTPS bytes restart %d: size=%d error=%v", round, len(got), err)
		}
		pingCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		rtt, err := session.Ping(pingCtx)
		stop()
		if err != nil {
			session.Close()
			t.Fatal("actual mailbox roundtrip", err)
		}
		t.Logf("restart=%d startup_ms=%d https_upload_bytes=%d https_download_bytes=%d mailbox_ping_ms=%d", round, startupMS, len(payload), len(got), rtt.Milliseconds())
		baseTransport.CloseIdleConnections()
		session.Close()
	}
	if cleanupCalls.Load() != 0 {
		t.Fatal("delivery called cleanup", cleanupCalls.Load())
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown did not join")
	}
}

type noCleanupSession struct {
	imapserver.Session
	calls *atomic.Int64
}

func (s noCleanupSession) Expunge(*imapserver.ExpungeWriter, *imap.UIDSet) error {
	s.calls.Add(1)
	return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "cleanup unavailable"}
}
func (s noCleanupSession) Delete(string) error {
	s.calls.Add(1)
	return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "cleanup unavailable"}
}

func applicationMailServer(t *testing.T) ([]MailAccount, *atomic.Int64) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spec := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, spec, spec, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	backend := imapmemserver.New()
	calls := &atomic.Int64{}
	var accounts []MailAccount
	for n := 0; n < 2; n++ {
		username := fmt.Sprintf("fixture%d", n)
		backend.AddUser(imapmemserver.NewUser(username, "fixture-private"))
		accounts = append(accounts, MailAccount{ID: username, Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: username, Password: "fixture-private", Roots: roots})
	}
	server := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return noCleanupSession{backend.NewSession(), calls}, nil, nil
	}})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	return accounts, calls
}
