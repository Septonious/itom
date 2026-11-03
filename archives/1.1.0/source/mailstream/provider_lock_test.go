package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
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

type deletionLockFixture struct {
	accounts     sync.Map
	release      chan struct{}
	deletes      atomic.Int64
	uidExpunges  atomic.Int64
	baseExpunges atomic.Int64
	folders      sync.Map
}
type deletionLockSession struct {
	imapserver.Session
	owner *deletionLockFixture
	user  string
}

func (s *deletionLockSession) Login(user, password string) error {
	s.user = user
	return s.Session.Login(user, password)
}
func (s *deletionLockSession) wait() {
	if _, blocked := s.owner.accounts.Load(s.user); blocked {
		<-s.owner.release
	}
}
func (s *deletionLockSession) Delete(name string) error {
	s.owner.deletes.Add(1)
	s.owner.accounts.Store(s.user, true)
	<-s.owner.release
	return s.Session.Delete(name)
}

// The historical provider capture timed out on UID EXPUNGE after a successful
// STORE. Model that command as an account-wide lock too. Ordinary EXPUNGE
// reaches this independent IMAP server with a nil UID set.
func (s *deletionLockSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if uids != nil {
		s.owner.uidExpunges.Add(1)
		s.owner.accounts.Store(s.user, true)
		<-s.owner.release
	} else {
		s.owner.baseExpunges.Add(1)
	}
	return s.Session.Expunge(w, uids)
}
func (s *deletionLockSession) Create(name string, o *imap.CreateOptions) error {
	s.wait()
	s.owner.folders.Store(s.user+"\x00"+name, true)
	return s.Session.Create(name, o)
}
func (s *deletionLockSession) Select(name string, o *imap.SelectOptions) (*imap.SelectData, error) {
	s.wait()
	return s.Session.Select(name, o)
}
func (s *deletionLockSession) Poll(w *imapserver.UpdateWriter, expunge bool) error {
	s.wait()
	return s.Session.Poll(w, expunge)
}
func (s *deletionLockSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	s.wait()
	return s.Session.Fetch(w, set, o)
}

// A timed-out DELETE can continue inside the provider and stall the account's
// other connections. Closing only the maintenance TLS socket does not undo it.
// Require a real duplex HTTPS transfer across repeated mailbox turnovers,
// followed by an immediate user disconnect and a fresh authenticated HTTPS.
func TestLoadAndReconnectWithAccountWideDeletionLocks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	fixture := &deletionLockFixture{release: make(chan struct{})}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &deletionLockSession{Session: s, owner: fixture} })
	t.Cleanup(func() { close(fixture.release) })
	server, err := NewStoredIMAPLink(ctx, "deletion-lock", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	clientState := t.TempDir()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, server, "deletion-lock", key, (&net.Dialer{}).DialContext, nil) }()
	payload := make([]byte, 36<<20)
	if _, err = rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			http.Error(w, "duplex", 500)
			return
		}
		_, _ = io.Copy(w, r.Body)
	}))
	defer origin.Close()
	for cycle := 0; cycle < 2; cycle++ {
		client, err := NewStoredIMAPLink(ctx, "deletion-lock", accounts, true, clientState)
		if err != nil {
			t.Fatal(err)
		}
		startup, stop := context.WithTimeout(ctx, 5*time.Second)
		session, err := Connect(startup, client, "deletion-lock", key)
		stop()
		if err != nil {
			client.Close()
			t.Fatalf("cycle %d reconnect: %v", cycle, err)
		}
		transport := origin.Client().Transport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = false
		transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) { return session.OpenTCP(ctx, address) }
		object := payload
		if cycle > 0 {
			object = payload[:123456]
		}
		// This is a byte-integrity and ownership check, including under -race
		// on a two-core host. Performance has a separate uninstrumented runner.
		response, requestErr := (&http.Client{Transport: transport, Timeout: 90 * time.Second}).Post(origin.URL, "application/octet-stream", bytes.NewReader(object))
		var body []byte
		if requestErr == nil {
			body, requestErr = io.ReadAll(response.Body)
			response.Body.Close()
		}
		if cycle == 0 && requestErr == nil {
			for _, line := range client.Snapshot().Lines {
				if line.DataGeneration < 2 {
					t.Fatalf("fixture did not cross multiple logical generations: %+v", line)
				}
			}
		}
		transport.CloseIdleConnections()
		session.Close()
		client.Close()
		if requestErr != nil || !bytes.Equal(body, object) {
			t.Fatalf("cycle %d TLS transfer failed after provider metadata lock: %v bytes=%d deletes=%d", cycle, requestErr, len(body), fixture.deletes.Load())
		}
	}
	folderCount := 0
	fixture.folders.Range(func(key, value any) bool {
		if !strings.Contains(key.(string), "-control-") {
			folderCount++
		}
		return true
	})
	if folderCount > len(accounts)*2*int(dataSlots)*2 {
		t.Fatalf("DATA folder count grew with throughput: %d", folderCount)
	}
	t.Logf("verified_bytes_each_direction=%d unique_DATA_folders=%d active_folder_deletes=%d uid_expunges=%d baseExpunges=%d", len(payload), folderCount, fixture.deletes.Load(), fixture.uidExpunges.Load(), fixture.baseExpunges.Load())
	if fixture.deletes.Load() != 0 || fixture.uidExpunges.Load() != 0 || fixture.baseExpunges.Load() == 0 {
		t.Fatal("reclamation must use base EXPUNGE without DELETE or UID EXPUNGE")
	}
	if server.Snapshot().Failed != 0 {
		t.Fatal("load failed carrier accounts")
	}
	cancel()
	server.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server shutdown did not join")
	}
}
