package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

// Real providers can serialize mailbox metadata across connections belonging
// to one account. Separate sockets do not isolate CREATE from an old-folder
// EXAMINE/SELECT. Model that state after a process restart, not a fresh inbox.
type serializedNamespace struct {
	imapserver.Session
	owner *namespaceFixture
	user  string
}
type namespaceFixture struct {
	locks   sync.Map
	old     atomic.Value
	started chan struct{}
	skew    bool
}

func (s *serializedNamespace) Login(user, password string) error {
	s.user = user
	return s.Session.Login(user, password)
}
func (s *serializedNamespace) lock() *sync.Mutex {
	v, _ := s.owner.locks.LoadOrStore(s.user, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (s *serializedNamespace) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if old, _ := s.owner.old.Load().(string); old != "" && strings.Contains(name, old) {
		mu := s.lock()
		mu.Lock()
		defer mu.Unlock()
		select {
		case s.owner.started <- struct{}{}:
		default:
		}
		// Backend work continues even if the maintenance client disconnects.
		delay := 3 * time.Second
		if s.owner.skew {
			delay = 150 * time.Millisecond
			if s.user == "fixture0" {
				delay = 4 * time.Second
			}
		}
		time.Sleep(delay)
	}
	return s.Session.Select(name, options)
}
func (s *serializedNamespace) Create(name string, options *imap.CreateOptions) error {
	mu := s.lock()
	mu.Lock()
	defer mu.Unlock()
	return s.Session.Create(name, options)
}

func TestRestartWithSerializedHistoricalMailboxMetadata(t *testing.T) {
	for _, scenario := range []struct {
		name string
		skew bool
	}{{"simultaneous_recovery", false}, {"one_account_still_recovering", true}} {
		t.Run(scenario.name, func(t *testing.T) { restartWithSerializedMetadata(t, scenario.skew) })
	}
}

func restartWithSerializedMetadata(t *testing.T, skew bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	provider := &namespaceFixture{started: make(chan struct{}, 8), skew: skew}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
		return &serializedNamespace{Session: s, owner: provider}
	})
	serverState, clientState := t.TempDir(), t.TempDir()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("verified HTTPS after retained-mail recovery\n"), 8192)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer origin.Close()
	for cycle := 0; cycle < 2; cycle++ {
		server, err := NewStoredIMAPLink(ctx, "serialized", accounts, false, serverState)
		if err != nil {
			t.Fatal(err)
		}
		client, err := NewStoredIMAPLink(ctx, "serialized", accounts, true, clientState)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- Serve(ctx, server, "serialized", key, (&net.Dialer{}).DialContext, nil) }()
		startup, stop := context.WithTimeout(ctx, 12*time.Second)
		session, err := Connect(startup, client, "serialized", key)
		stop()
		if err != nil {
			client.Close()
			server.Close()
			t.Fatalf("cycle=%d startup=%v client=%+v server=%+v client-events=%+v server-events=%+v", cycle, err, client.Snapshot(), server.Snapshot(), client.Events(), server.Events())
		}
		if cycle == 1 {
			if skew {
				allReady := true
				for _, line := range server.Snapshot().Lines {
					allReady = allReady && line.Ready
				}
				if allReady {
					t.Fatal("startup waited for the recovering account instead of using the ready peer")
				}
			}
			select {
			case <-provider.started:
			case <-time.After(5 * time.Second):
				t.Fatal("historical metadata fixture not exercised")
			}
		}
		transport := origin.Client().Transport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = false
		transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
		response, requestErr := (&http.Client{Transport: transport, Timeout: 2 * time.Second}).Get(origin.URL)
		var body []byte
		if requestErr == nil {
			body, requestErr = io.ReadAll(response.Body)
			response.Body.Close()
		}
		transport.CloseIdleConnections()
		id := session.cipher.id
		session.Close()
		client.Close()
		server.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("shutdown did not release mailbox roles")
		}
		if requestErr != nil || !bytes.Equal(body, payload) {
			t.Fatalf("cycle=%d HTTPS during recovery: %v bytes=%d", cycle, requestErr, len(body))
		}
		if cycle == 0 {
			provider.old.Store("-" + hex.EncodeToString(id[:]) + "-s2c-")
		}
	}
}
