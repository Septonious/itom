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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type poweredOffDialer struct {
	mu          sync.Mutex
	connections []net.Conn
	offline     bool
}

func (d *poweredOffDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.offline {
		return nil, net.ErrClosed
	}
	c, err := (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}).DialContext(ctx, network, address)
	if err == nil {
		d.connections = append(d.connections, c)
	}
	return c, err
}
func (d *poweredOffDialer) powerOff() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.offline = true
	for _, c := range d.connections {
		_ = c.Close()
	}
}

type cleanupRoleSession struct {
	imapserver.Session
	client                           bool
	clientMutations, serverMutations *atomic.Int64
}

func (s *cleanupRoleSession) mutation() {
	if s.client {
		s.clientMutations.Add(1)
	} else {
		s.serverMutations.Add(1)
	}
}
func (s *cleanupRoleSession) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	s.mutation()
	return s.Session.Store(w, set, f, o)
}
func (s *cleanupRoleSession) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	s.mutation()
	return s.Session.Expunge(w, uids)
}
func (s *cleanupRoleSession) Delete(folder string) error {
	s.mutation()
	return errors.New("folder deletion is forbidden")
}

// Cut the phone's underlying sockets without Session.Close, Link.Close, a final
// acknowledgement or a new client handshake. Only the standing server can drain
// partial DATA and control tails, and unrelated mail must remain untouched.
func TestServerCleansOwnedMailAfterAbruptClientPowerLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	var clientMutations, serverMutations atomic.Int64
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 2, func(s imapserver.Session, c *imapserver.Conn) imapserver.Session {
		ip, _, _ := net.SplitHostPort(c.NetConn().RemoteAddr().String())
		return &cleanupRoleSession{s, ip == "127.0.0.2", &clientMutations, &serverMutations}
	})
	server, err := NewStoredIMAPLink(ctx, "offline-cleanup", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	phone := &poweredOffDialer{}
	phoneAccounts := append([]MailAccount(nil), accounts...)
	for i := range phoneAccounts {
		phoneAccounts[i].Dialer = phone
	}
	client, err := NewStoredIMAPLink(ctx, "offline-cleanup", phoneAccounts, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	go ServeWithOptions(ctx, server, "offline-cleanup", key, (&net.Dialer{}).DialContext, ServerOptions{IdleTimeout: 2 * time.Second})
	session, err := Connect(ctx, client, "offline-cleanup", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(w, r.Body) }))
	defer origin.Close()
	tr := origin.Client().Transport.(*http.Transport).Clone()
	defer tr.CloseIdleConnections()
	tr.DialContext = func(c context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(c, target) }
	payload := bytes.Repeat([]byte("phone traffic integrity"), 60000)
	res, err := (&http.Client{Transport: tr, Timeout: 15 * time.Second}).Post(origin.URL, "application/octet-stream", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || !bytes.Equal(body, payload) {
		t.Fatal("HTTPS integrity", err)
	}
	probe, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer probe.close()
	if err = probe.client.Create("personal-keep", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	cmd, err := probe.startAppend("personal-keep", []byte("Subject: personal\r\n\r\nkeep"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	names, err := probe.client.List("", server.folderPrefix()+"-*", nil).Collect()
	if err != nil || len(names) < 3 {
		t.Fatal("missing partial mail fixture", err)
	}
	present := uint32(0)
	for _, n := range names {
		s, e := probe.client.Status(n.Mailbox, &imap.StatusOptions{NumMessages: true}).Wait()
		if e != nil || s.NumMessages == nil {
			t.Fatal(e)
		}
		present += *s.NumMessages
	}
	if present == 0 {
		t.Fatal("power loss fixture contains no retained mail")
	}
	before := serverMutations.Load()
	phone.powerOff()
	waitRetention(t, ctx, func() bool { server.mu.Lock(); defer server.mu.Unlock(); return server.attached == (SessionID{}) }, "server did not expire unreachable phone")
	waitRetention(t, ctx, func() bool {
		for _, a := range accounts {
			p, e := openMailPipe(ctx, a)
			if e != nil {
				return false
			}
			folders, e := p.client.List("", server.folderPrefix()+"-*", nil).Collect()
			empty := e == nil
			for _, f := range folders {
				s, e := p.client.Status(f.Mailbox, &imap.StatusOptions{NumMessages: true}).Wait()
				if e != nil || s.NumMessages == nil || *s.NumMessages != 0 {
					empty = false
					break
				}
			}
			p.close()
			if !empty {
				return false
			}
		}
		return true
	}, "server did not empty owned partial DATA/control with phone offline")
	if clientMutations.Load() != 0 || serverMutations.Load() <= before {
		t.Fatal("cleanup depended on phone or never ran on server", clientMutations.Load(), serverMutations.Load(), before)
	}
	status, err := probe.client.Status("personal-keep", &imap.StatusOptions{NumMessages: true}).Wait()
	if err != nil || status.NumMessages == nil || *status.NumMessages != 1 {
		t.Fatal("unrelated mail changed", err)
	}
	t.Logf("retained=%d; client mutations=0; server mutations after power loss=%d; no client close/ack/reconnect needed", present, serverMutations.Load()-before)
}
