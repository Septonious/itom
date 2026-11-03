package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type cleanupFault struct {
	mu           sync.Mutex
	tail         string
	closeSeen    atomic.Bool
	baseAttempts atomic.Int64
	blockBase    bool
	owners       map[string]bool
	release      chan struct{}
}

func (f *cleanupFault) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Only fixture command names are observed. No wire contents are exported.
	joined := f.tail + string(p)
	if strings.Contains(joined, " CLOSE\r\n") {
		f.closeSeen.Store(true)
	}
	if len(joined) > 128 {
		joined = joined[len(joined)-128:]
	}
	f.tail = joined
	return len(p), nil
}

type cleanupFaultSession struct {
	imapserver.Session
	fault *cleanupFault
	user  string
}

func (s *cleanupFaultSession) Login(user, password string) error {
	s.user = user
	return s.Session.Login(user, password)
}

func (s *cleanupFaultSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	s.fault.baseAttempts.Add(1)
	s.fault.mu.Lock()
	if s.fault.owners == nil {
		s.fault.owners = map[string]bool{}
	}
	s.fault.owners[s.user] = true
	s.fault.mu.Unlock()
	if s.fault.blockBase || s.fault.closeSeen.Load() || uids != nil {
		<-s.fault.release
	}
	return s.Session.Expunge(w, uids)
}

// Field order matters: download, a new upload TLS connection, cancellation and
// immediate VPN replacement. CLOSE hangs like the field provider. In the second
// case even optional EXPUNGE hangs: no receiver may be canceled because of it.
func TestDownloadUploadAndReconnectDespiteProviderCleanupFaults(t *testing.T) {
	for _, blockBase := range []bool{false, true} {
		name := "CLOSE_stalls"
		if blockBase {
			name = "all_reclamation_stalls"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			f := &cleanupFault{blockBase: blockBase, release: make(chan struct{})}
			accounts, _ := applicationMailServerObserved(t, true, false, func(s imapserver.Session) imapserver.Session { return &cleanupFaultSession{Session: s, fault: f} }, f)
			t.Cleanup(func() { close(f.release) })
			server, err := NewStoredIMAPLink(ctx, "cleanup-field", accounts, false, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			key := make([]byte, 32)
			if _, err = rand.Read(key); err != nil {
				t.Fatal(err)
			}
			go Serve(ctx, server, "cleanup-field", key, (&net.Dialer{}).DialContext, nil)
			state := t.TempDir()
			client, err := NewStoredIMAPLink(ctx, "cleanup-field", accounts, true, state)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if client != nil {
					client.Close()
				}
			}()
			session, err := Connect(ctx, client, "cleanup-field", key)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if session != nil {
					session.Close()
				}
			}()
			payload := make([]byte, 24<<20)
			if _, err = rand.Read(payload); err != nil {
				t.Fatal(err)
			}
			origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					body, e := io.ReadAll(r.Body)
					if e != nil || !bytes.Equal(body, payload) {
						http.Error(w, "upload integrity", 500)
						return
					}
					io.WriteString(w, "verified upload")
					return
				}
				w.Write(payload)
			}))
			var transports []*http.Transport
			defer func() {
				cancel()
				for _, tr := range transports {
					tr.CloseIdleConnections()
				}
				origin.Close()
			}()
			transport := func(s *Session) *http.Transport {
				tr := origin.Client().Transport.(*http.Transport).Clone()
				tr.ForceAttemptHTTP2 = false
				tr.DisableKeepAlives = true
				tr.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) { return s.OpenTCP(ctx, address) }
				transports = append(transports, tr)
				return tr
			}
			tr := transport(session)
			httpClient := &http.Client{Transport: tr, Timeout: 90 * time.Second}
			for range 8 {
				c, e := session.OpenTCP(ctx, origin.Listener.Addr().String())
				if e != nil {
					t.Fatal(e)
				}
				c.Write([]byte{0x16, 0x03, 0x01})
				c.Close()
			}
			response, err := httpClient.Get(origin.URL)
			if err != nil {
				t.Fatal("download startup:", err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatalf("download failed: bytes=%d err=%v", len(body), err)
			}
			response, err = httpClient.Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
			if err != nil {
				t.Fatal("upload startup:", err)
			}
			body, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(body) != "verified upload" {
				t.Fatalf("upload failed: %q %v", body, err)
			}
			if client.Snapshot().Failed != 0 || server.Snapshot().Failed != 0 {
				t.Fatal("cleanup retired working accounts")
			}
			if f.closeSeen.Load() {
				t.Fatal("delivery or housekeeping attempted CLOSE")
			}
			if blockBase {
				f.mu.Lock()
				owners := len(f.owners)
				f.mu.Unlock()
				if !server.Snapshot().Reclamation.Paused || f.baseAttempts.Load() < 1 || owners != 1 {
					t.Fatalf("cleanup circuit breaker did not contain the failure: %+v attempts=%d", server.Snapshot().Reclamation, f.baseAttempts.Load())
				}
			} else if server.Snapshot().Reclamation.Completed == 0 {
				t.Fatal("healthy base EXPUNGE was not exercised")
			}
			if !blockBase && (client.Snapshot().Retired == 0 || server.Snapshot().Retired == 0) {
				t.Fatal("verified collection did not release capacity in both directions")
			}
			session.Close()
			client.Close()
			client, err = NewStoredIMAPLink(ctx, "cleanup-field", accounts, true, state)
			if err != nil {
				t.Fatal(err)
			}
			startup, stop := context.WithTimeout(ctx, 4*time.Second)
			session, err = Connect(startup, client, "cleanup-field", key)
			stop()
			if err != nil {
				t.Fatal("reconnect after load:", err)
			}
			response, err = (&http.Client{Transport: transport(session), Timeout: 90 * time.Second}).Get(origin.URL)
			if err != nil {
				t.Fatal("fresh HTTPS after reconnect:", err)
			}
			body, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(body, payload) {
				t.Fatal("reconnected HTTPS integrity", err)
			}
			if client.Snapshot().Failed != 0 || server.Snapshot().Failed != 0 {
				t.Fatal("replacement lost accounts")
			}
			t.Logf("download=%d upload=%d reconnect_download=%d aborted_TLS=8 failed_accounts=0 cleanup=%+v", len(payload), len(payload), len(body), server.Snapshot().Reclamation)
			if e := session.Err(); e != nil && !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
		})
	}
}
