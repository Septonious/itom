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
	"strings"
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
func TestApplicationHTTPSAndUserRestarts(t *testing.T) {
	for _, maintenance := range []bool{true, false} {
		name := "retirement_available"
		if !maintenance {
			name = "retirement_refused"
		}
		t.Run(name, func(t *testing.T) { runApplication(t, maintenance) })
	}
}
func runApplication(t *testing.T, maintenance bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	accounts, cleanupCalls := applicationMailServer(t, maintenance)
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
	var serverSession atomic.Pointer[Session]
	go func() {
		serverDone <- Serve(ctx, serverLink, "application", psk, (&net.Dialer{}).DialContext, func(s *Session) { serverSession.Store(s) })
	}()
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
			t.Logf("failed state client=%+v client_mail=%+v server_mail=%+v session_error=%v", session.Snapshot(), clientLink.Snapshot(), serverLink.Snapshot(), session.Err())
			dumpPending(t, "client", session)
			if peer := serverSession.Load(); peer != nil {
				t.Logf("server_session=%+v error=%v", peer.Snapshot(), peer.Err())
				dumpPending(t, "server", peer)
				peer.mu.Lock()
				missing := map[uint64]bool{}
				for seq := range peer.pending {
					missing[seq] = true
				}
				peer.mu.Unlock()
				for _, event := range serverLink.Events() {
					if event.Session == peer.cipher.id && missing[event.Sequence] {
						t.Logf("server event=%+v", event)
					}
				}
				for _, event := range clientLink.Events() {
					if event.Session == peer.cipher.id && missing[event.Sequence] {
						t.Logf("client event=%+v", event)
					}
				}
			}
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
	if cleanupCalls.expunge.Load() != 0 {
		t.Fatal("delivery used EXPUNGE", cleanupCalls.expunge.Load())
	}
	if cleanupCalls.delete.Load() == 0 {
		t.Fatal("segment retirement never exercised")
	}
	retired := clientLink.Snapshot().Retired + serverLink.Snapshot().Retired
	if maintenance && retired == 0 {
		t.Fatal("successful deletion never released its own new segment storage")
	}
	if !maintenance && retired != 0 {
		t.Fatal("refused deletion released storage", retired)
	}
	t.Logf("segment_delete_attempts=%d verified_retirements=%d", cleanupCalls.delete.Load(), retired)
	if !maintenance && clientLink.Snapshot().RetainedBytes == 0 {
		t.Fatal("refused maintenance erased retention debt")
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown did not join")
	}
}
func dumpPending(t *testing.T, label string, s *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for seq, p := range s.pending {
		plain, err := s.cipher.send.Decrypt(nil, seq, p.wire[:headerSize], p.wire[headerSize:])
		if err != nil {
			t.Log(label, err)
			continue
		}
		f, _ := decodeFrame(plain)
		t.Logf("%s pending seq=%d kind=%d stream=%d offset=%d bytes=%d attempts=%d age_ms=%d", label, seq, f.kind, f.stream, f.offset, len(f.data), p.attempts, time.Since(p.sent).Milliseconds())
	}
}

type noCleanupSession struct {
	imapserver.Session
	calls        *cleanupCounters
	maintenance  bool
	appendCounts map[string]int
}
type cleanupCounters struct{ expunge, delete atomic.Int64 }

func (s noCleanupSession) Expunge(*imapserver.ExpungeWriter, *imap.UIDSet) error {
	s.calls.expunge.Add(1)
	return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "cleanup unavailable"}
}
func (s noCleanupSession) Delete(folder string) error {
	s.calls.delete.Add(1)
	if s.maintenance {
		return s.Session.Delete(folder)
	}
	return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "cleanup unavailable"}
}
func (s noCleanupSession) Append(folder string, r imap.LiteralReader, opts *imap.AppendOptions) (*imap.AppendData, error) {
	data, err := s.Session.Append(folder, r, opts)
	if err == nil && s.maintenance && !strings.Contains(folder, "-control-") {
		s.appendCounts[folder]++
		if s.appendCounts[folder] == segmentAppends {
			// The stored BODY is visible to the other TLS connection before
			// this APPEND's command completion. Retirement must drain that
			// transaction even when delivery receipts have already arrived.
			time.Sleep(300 * time.Millisecond)
		}
	}
	return data, err
}

func applicationMailServer(t *testing.T, maintenance bool) ([]MailAccount, *cleanupCounters) {
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
	calls := &cleanupCounters{}
	var accounts []MailAccount
	for n := 0; n < 2; n++ {
		username := fmt.Sprintf("fixture%d", n)
		backend.AddUser(imapmemserver.NewUser(username, "fixture-private"))
		accounts = append(accounts, MailAccount{ID: username, Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: username, Password: "fixture-private", Roots: roots})
	}
	server := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}}, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		return noCleanupSession{backend.NewSession(), calls, maintenance, map[string]int{}}, nil, nil
	}})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Close(); <-done })
	return accounts, calls
}
