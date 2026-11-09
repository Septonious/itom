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

type missingBootstrapFixture struct {
	release                                       chan struct{}
	missingSelect, missingStatus, redundantCreate atomic.Int32
}

type missingBootstrapSession struct {
	imapserver.Session
	f *missingBootstrapFixture
}

func bootstrapControl(name string) bool {
	return strings.HasSuffix(name, "-c-u") || strings.HasSuffix(name, "-c-d")
}

func (s *missingBootstrapSession) Select(name string, o *imap.SelectOptions) (*imap.SelectData, error) {
	if bootstrapControl(name) {
		if _, err := s.Session.Status(name, &imap.StatusOptions{}); err != nil {
			s.f.missingSelect.Add(1)
			<-s.f.release
		}
	}
	return s.Session.Select(name, o)
}

func (s *missingBootstrapSession) Status(name string, o *imap.StatusOptions) (*imap.StatusData, error) {
	if bootstrapControl(name) {
		if _, err := s.Session.Status(name, &imap.StatusOptions{}); err != nil {
			s.f.missingStatus.Add(1)
			<-s.f.release
		}
	}
	return s.Session.Status(name, o)
}

func (s *missingBootstrapSession) Create(name string, o *imap.CreateOptions) error {
	if bootstrapControl(name) {
		if _, err := s.Session.Status(name, &imap.StatusOptions{}); err == nil {
			s.f.redundantCreate.Add(1)
			<-s.f.release
		}
	}
	return s.Session.Create(name, o)
}

// First five accounts have no transport folders, while the last five retain
// existing ones. SELECT/STATUS on a missing folder and redundant CREATE on an
// existing folder stall independently. Bootstrap must avoid all three traps.
func TestFirstFiveEmptyAccountsBootstrapWithoutMissingFolderSelection(t *testing.T) {
	f := &missingBootstrapFixture{release: make(chan struct{})}
	accounts, _ := applicationMailServerAccounts(t, true, false, func(s imapserver.Session) imapserver.Session {
		return &missingBootstrapSession{Session: s, f: f}
	}, nil, 10)
	t.Cleanup(func() { close(f.release) })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for n, account := range accounts {
		p, err := openMailPipe(ctx, account)
		if err != nil {
			t.Fatal(err)
		}
		if err = p.client.Create("Personal keep", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		cmd, err := p.startAppend("Personal keep", []byte("Subject: unrelated mail\r\n\r\nkeep"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		if n >= 5 {
			for _, out := range []bool{false, true} {
				name := (&IMAPLink{identity: "missingbootstrap", client: true}).controlFolder(out)
				if err = p.client.Create(name, nil).Wait(); err != nil {
					t.Fatal(err)
				}
			}
		}
		p.close()
	}
	server, err := NewIMAPLink(ctx, "missingbootstrap", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	readyBy := time.NewTimer(3 * time.Second)
	defer readyBy.Stop()
	for server.Snapshot().Ready != len(accounts) {
		select {
		case <-readyBy.C:
			t.Fatalf("control folders did not bootstrap: ready=%d/%d missing_select=%d missing_status=%d redundant_create=%d", server.Snapshot().Ready, len(accounts), f.missingSelect.Load(), f.missingStatus.Load(), f.redundantCreate.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	client, err := NewIMAPLink(ctx, "missingbootstrap", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	go Serve(ctx, server, "missingbootstrap", bytes.Repeat([]byte{5}, 32), (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "missingbootstrap", bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	waitRetention(t, ctx, func() bool { return server.Snapshot().Data == 10 && client.Snapshot().Data == 10 }, "not all thirty account roles joined")
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, r.Body) }))
	defer origin.Close()
	tr := origin.Client().Transport.(*http.Transport).Clone()
	tr.ForceAttemptHTTP2 = false
	tr.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	defer tr.CloseIdleConnections()
	payload := bytes.Repeat([]byte("mixed clean and existing accounts\n"), 32768)
	response, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("HTTPS integrity", err, len(got))
	}
	for _, account := range accounts {
		p, err := openMailPipe(ctx, account)
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.client.Status("Personal keep", &imap.StatusOptions{NumMessages: true}).Wait()
		p.close()
		if err != nil || d.NumMessages == nil || *d.NumMessages != 1 {
			t.Fatal("unrelated mail changed", err)
		}
	}
	if f.missingSelect.Load() != 0 || f.missingStatus.Load() != 0 || f.redundantCreate.Load() != 0 {
		t.Fatal("bootstrap used blocking metadata commands", f.missingSelect.Load(), f.missingStatus.Load(), f.redundantCreate.Load())
	}
}
