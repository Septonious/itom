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
// real TCP, real HTTPS, a body larger than initial credit, then user restarts.
// CLOSE reclamation is performed on the selected reader and never opens
// another account connection or deletes a folder during delivery.
func TestApplicationHTTPSAndUserRestarts(t *testing.T) {
	runApplication(t, true, false)
}
func runApplication(t *testing.T, maintenance, stalled bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	accounts, cleanupCalls := applicationMailServer(t, maintenance, stalled)
	serverLink, err := NewStoredIMAPLink(ctx, "application", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer serverLink.Close()
	clientState := t.TempDir()
	clientLink, err := NewStoredIMAPLink(ctx, "application", accounts, true, clientState)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { clientLink.Close() }()
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
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			http.Error(w, "fixture duplex unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(w, r.Body)
	}))
	defer func() { cancel(); origin.Close() }()
	// An application-sized object independent of controller constants.
	// A 20 MB application object crosses full retained segments even when
	// publication coalescing is effective. The old 4 MB object exercised
	// only a partial segment after the throughput improvements.
	payload := make([]byte, 20_123_456)
	if _, err = rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		// The first transfer crosses complete retained segments. Restart
		// checks need a fresh TLS handshake and an object beyond initial
		// credit, not three copies of the same large-object stress test.
		payload := payload
		if round > 0 {
			payload = payload[:1_234_567]
		}
		start := time.Now()
		startup, stop := context.WithTimeout(ctx, 10*time.Second)
		session, err := Connect(startup, clientLink, "application", psk)
		stop()
		if err != nil {
			t.Fatalf("restart %d: %v client=%+v server=%+v", round, err, clientLink.Snapshot(), serverLink.Snapshot())
		}
		startupMS := time.Since(start).Milliseconds()
		if round == 0 {
			applicationHalfClose(t, session)
		}
		baseTransport := origin.Client().Transport.(*http.Transport).Clone()
		baseTransport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
		baseTransport.ForceAttemptHTTP2 = false
		client := &http.Client{Transport: baseTransport, Timeout: 60 * time.Second}
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
		if stalled && rtt > 300*time.Millisecond {
			t.Fatalf("stalled maintenance delayed control ping: %s", rtt)
		}
		t.Logf("restart=%d startup_ms=%d https_upload_bytes=%d https_download_bytes=%d mailbox_ping_ms=%d", round, startupMS, len(payload), len(got), rtt.Milliseconds())
		baseTransport.CloseIdleConnections()
		session.Close()
		if round < 2 {
			if err := clientLink.Close(); err != nil {
				t.Fatal("retention checkpoint", err)
			}
			prior := clientLink.Snapshot().RetainedBytes
			clientLink, err = NewStoredIMAPLink(ctx, "application", accounts, true, clientState)
			if err != nil {
				t.Fatal("retention reopen", err)
			}
			if recovered := clientLink.Snapshot().RecoveredBytes; recovered != prior || recovered == 0 {
				t.Fatalf("restart forgot retained publication: recovered=%d want=%d", recovered, prior)
			} else {
				t.Logf("fresh runtime recovered %d retained bytes from durable state", recovered)
			}
		}
	}
	if cleanupCalls.delete.Load() != 0 {
		t.Fatal("live delivery attempted folder deletion")
	}
	if cleanupCalls.expunge.Load() == 0 {
		t.Fatal("consumed-slot CLOSE reclamation was not exercised")
	}
	retired := clientLink.Snapshot().Retired + serverLink.Snapshot().Retired
	if retired == 0 {
		t.Fatal("verified slot reclamation never released capacity")
	}
	t.Logf("consumed_slot_close_commands=%d reclaimed_generations=%d", cleanupCalls.expunge.Load(), retired)
	cancel()
	select {
	case <-serverDone:
	case <-time.After(3 * time.Second):
		t.Fatal("server shutdown did not join")
	}
	if err := clientLink.Close(); err != nil {
		t.Fatal(err)
	}
	charged := clientLink.Snapshot().RetainedBytes
	aliases := append([]MailAccount(nil), accounts...)
	for i := range aliases {
		aliases[i].ID = "renamed_" + aliases[i].ID
	}
	// No new session is started here. Re-exporting the same physical account
	// under another routing alias must reopen the same durable debt.
	rebound, err := NewStoredIMAPLink(context.Background(), "application", aliases, true, clientState)
	if err != nil {
		t.Fatal(err)
	}
	defer rebound.Close()
	if recovered := rebound.Snapshot().RecoveredBytes; recovered != charged {
		t.Fatalf("routing alias reset physical-account debt: got=%d want=%d", recovered, charged)
	}
	t.Logf("routing aliases preserve %d retained bytes", charged)
}

// A peer that waits for request EOF before replying catches response truncation
// from premature RESET. The response crosses the initial receive-credit limit.
func applicationHalfClose(t *testing.T, session *Session) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	request := []byte("request complete only at TCP half-close")
	response := bytes.Repeat([]byte{0x93, 0x17, 0xa5, 0x08}, 256<<10)
	finished := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			finished <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		got, err := io.ReadAll(io.LimitReader(conn, int64(len(request)+1)))
		if err == nil && !bytes.Equal(got, request) {
			err = fmt.Errorf("half-close request bytes=%d", len(got))
		}
		if err == nil {
			_, err = io.Copy(conn, bytes.NewReader(response))
		}
		finished <- err
	}()
	ctx, cancel := context.WithTimeout(session.ctx, 8*time.Second)
	defer cancel()
	stream, err := session.OpenTCP(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = stream.Write(request); err != nil {
		t.Fatal(err)
	}
	if err = stream.(*Stream).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(io.LimitReader(stream, int64(len(response)+1)))
	if err != nil || !bytes.Equal(got, response) {
		t.Fatalf("half-close response bytes=%d want=%d error=%v", len(got), len(response), err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	t.Logf("TCP half-close preserved %d response bytes and EOF", len(got))
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
type cleanupCounters struct {
	expunge, delete atomic.Int64
	stalled         bool
	release         chan struct{}
}

func (s noCleanupSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	s.calls.expunge.Add(1)
	if uids != nil {
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "UID EXPUNGE forbidden; CLOSE required"}
	}
	return s.Session.Expunge(w, uids)
}
func (s noCleanupSession) Delete(folder string) error {
	s.calls.delete.Add(1)
	if s.calls.stalled {
		<-s.calls.release
	}
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

func applicationMailServer(t *testing.T, maintenance, stalled bool) ([]MailAccount, *cleanupCounters) {
	return applicationMailServerWrapped(t, maintenance, stalled, nil)
}

func applicationMailServerWrapped(t *testing.T, maintenance, stalled bool, wrap func(imapserver.Session) imapserver.Session) ([]MailAccount, *cleanupCounters) {
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
	calls := &cleanupCounters{stalled: stalled, release: make(chan struct{})}
	var accounts []MailAccount
	for n := 0; n < 2; n++ {
		username := fmt.Sprintf("fixture%d", n)
		backend.AddUser(imapmemserver.NewUser(username, "fixture-private"))
		accounts = append(accounts, MailAccount{ID: username, Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: username, Password: "fixture-private", Roots: roots})
	}
	server := imapserver.New(&imapserver.Options{Caps: imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIdle: {}, imap.CapUIDPlus: {}}, NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
		var session imapserver.Session = noCleanupSession{backend.NewSession(), calls, maintenance, map[string]int{}}
		if wrap != nil {
			session = wrap(session)
		}
		return session, nil, nil
	}})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { close(calls.release); server.Close(); <-done })
	return accounts, calls
}
