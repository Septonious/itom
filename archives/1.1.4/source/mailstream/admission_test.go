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
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type unevenControl struct {
	imapserver.Session
	user string
}

func (s *unevenControl) Login(user, password string) error {
	s.user = user
	return s.Session.Login(user, password)
}
func (s *unevenControl) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	if s.user == "fixture1" && (strings.Contains(name, "-control-") || strings.HasSuffix(name, "-c-u") || strings.HasSuffix(name, "-c-d")) {
		time.Sleep(3 * time.Second)
	}
	return s.Session.Select(name, options)
}

// Field RC3: the first mutually authenticated account has control headroom
// but no bulk capacity. Another account has capacity and joins later. No
// application packet arrives to teach the server that this route is ready.
func TestStartupWithUnevenRecoveredCapacity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &unevenControl{Session: s} })
	dir := t.TempDir()
	store, err := openRetention(dir, "admission", false)
	if err != nil {
		t.Fatal(err)
	}
	debt := prototypePhysicalBudget - reservationQuantum/2
	if err = store.put(retentionKey(accounts[0]), debt); err != nil {
		t.Fatal(err)
	}
	store.close()
	server, err := NewStoredIMAPLink(ctx, "admission", accounts, false, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "admission", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, server, "admission", key, (&net.Dialer{}).DialContext, nil) }()
	session, err := Connect(ctx, client, "admission", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// The real app verifies a 16 KiB echo before starting periodic pings.
	// HTTPS alone exchanges enough small TLS packets to hide this deadlock.
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, e := echo.Accept()
		if e == nil {
			defer c.Close()
			_, _ = io.Copy(c, c)
		}
	}()
	probe, err := session.OpenTCP(ctx, echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	_ = probe.SetDeadline(time.Now().Add(5 * time.Second))
	probeBody := bytes.Repeat([]byte("x"), 16<<10)
	if _, err = probe.Write(probeBody); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(probeBody))
	if _, err = io.ReadFull(probe, got); err != nil {
		t.Fatalf("startup echo deadlocked behind charged route: %v; server=%+v client=%+v", err, server.Snapshot(), client.Snapshot())
	}
	if !bytes.Equal(got, probeBody) {
		t.Fatal("startup echo corrupted")
	}
	probe.Close()
	payload := bytes.Repeat([]byte("verified HTTPS over the later account with free retained capacity\n"), 4096)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer origin.Close()
	transport := origin.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Get(origin.URL)
	if err != nil {
		t.Fatalf("startup HTTPS deadlocked behind charged account: %v; server=%+v client=%+v", err, server.Snapshot(), client.Snapshot())
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(body, payload) {
		t.Fatalf("HTTPS integrity: %v bytes=%d", err, len(body))
	}
	// RC7 may finish its verified empty-namespace recovery while HTTPS runs.
	// Before that proof, the recovered historical charge must remain intact.
	line := server.Snapshot().Lines[0]
	if line.RetainedBytes < debt && !line.StorageRecoveryReady {
		t.Fatalf("historical debt dropped before verified recovery: before=%d line=%+v", debt, line)
	}
	cancel()
	session.Close()
	client.Close()
	server.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server did not join")
	}
}
