package mailstream

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type existingCreateStall struct {
	imapserver.Session
	armed *atomic.Bool
	calls *atomic.Int32
	stop  <-chan struct{}
}

func (s existingCreateStall) Create(name string, options *imap.CreateOptions) error {
	if s.armed.Load() && (strings.Contains(name, "-control-") || strings.HasSuffix(name, "-c-u") || strings.HasSuffix(name, "-c-d")) {
		if _, err := s.Session.Status(name, &imap.StatusOptions{UIDNext: true}); err == nil {
			s.calls.Add(1)
			<-s.stop
		}
	}
	return s.Session.Create(name, options)
}

// LOGIN and ordinary mailbox reads can succeed while redundant CREATE on an
// existing provider folder is stuck. Restarting with the same folders must
// not repeat that mutation or require the user to delete mailboxes.
func TestExistingControlFoldersStartWithoutRedundantCreate(t *testing.T) {
	var armed atomic.Bool
	var calls atomic.Int32
	stop := make(chan struct{})
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
		return existingCreateStall{s, &armed, &calls, stop}
	})
	defer close(stop)
	for _, account := range accounts {
		p, err := openMailPipe(context.Background(), account)
		if err != nil {
			t.Fatal(err)
		}
		for _, out := range []bool{false, true} {
			if err := p.client.Create((&IMAPLink{identity: "existing", client: true}).controlFolder(out), nil).Wait(); err != nil {
				t.Fatal(err)
			}
		}
		p.close()
	}
	armed.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, client := range []bool{false, true} {
		link, err := NewIMAPLink(ctx, "existing", accounts, client)
		if err != nil {
			t.Fatal(err)
		}
		defer link.Close()
		for link.Snapshot().Ready < len(accounts) {
			select {
			case <-ctx.Done():
				t.Fatalf("existing folders did not start: %+v", link.Snapshot())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("repeated %d existing-folder CREATEs", calls.Load())
	}
}

type recoveringSelection struct {
	imapserver.Session
	user  string
	until time.Time
}

func (s *recoveringSelection) Login(user, password string) error {
	s.user = user
	return s.Session.Login(user, password)
}
func (s *recoveringSelection) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if s.user == "fixture1" && (strings.Contains(name, "-control-") || strings.HasSuffix(name, "-c-u") || strings.HasSuffix(name, "-c-d")) && time.Now().Before(s.until) {
		return nil, &imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable, Text: "temporary fixture mailbox unavailable"}
	}
	return s.Session.Select(name, options)
}

// Keep one account unavailable until HTTPS already works on another. It must
// join the same authenticated session when provider reads recover; neither a
// VPN restart nor replacement of a healthy account's sockets is allowed.
func TestUnavailableAccountJoinsRunningHTTPSWithoutVPNRestart(t *testing.T) {
	until := time.Now().Add(3 * time.Second)
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session {
		return &recoveringSelection{Session: s, until: until}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	server, err := NewIMAPLink(ctx, "recovering", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "recovering", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := bytes.Repeat([]byte{7}, 32)
	go Serve(ctx, server, "recovering", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "recovering", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "fixture body read failed", 500)
			return
		}
		_, _ = w.Write(body)
	}))
	defer origin.Close()
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) {
		return session.OpenTCP(ctx, target)
	}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	payload := bytes.Repeat([]byte("real TLS request across temporarily incomplete accounts\n"), 4096)
	request := func() {
		response, err := httpClient.Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("HTTPS failed: %v bytes=%d", err, len(got))
		}
	}
	request()
	for {
		c, s := client.Snapshot(), server.Snapshot()
		if c.Data == len(accounts) && s.Data == len(accounts) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("accounts never rejoined: client=%+v server=%+v", c, s)
		case <-time.After(20 * time.Millisecond):
		}
	}
	for _, link := range []*IMAPLink{client, server} {
		for _, line := range link.Snapshot().Lines {
			if line.Failure != "" || !line.Ready {
				t.Fatalf("recovered account stale failure: %+v", line)
			}
			if line.Account == "fixture0" && line.StartupAttempts != 1 {
				t.Fatalf("healthy account restarted: %+v", line)
			}
			if line.Account == "fixture1" && line.StartupAttempts < 2 {
				t.Fatalf("fixture did not retry: %+v", line)
			}
		}
	}
	request()
}
