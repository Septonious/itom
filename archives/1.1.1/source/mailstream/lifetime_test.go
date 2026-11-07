package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type shutdownDialer struct{ connection *shutdownConn }

func (d *shutdownDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.connection = &shutdownConn{Conn: c, closed: make(chan struct{})}
	return d.connection, nil
}

type shutdownConn struct {
	net.Conn
	blocked  atomic.Bool
	once     sync.Once
	closed   chan struct{}
	mu       sync.Mutex
	deadline time.Time
}

func (c *shutdownConn) Write(b []byte) (int, error) {
	if !c.blocked.Load() {
		return c.Conn.Write(b)
	}
	c.mu.Lock()
	deadline := c.deadline
	c.mu.Unlock()
	var timer *time.Timer
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		timer = time.NewTimer(max(0, time.Until(deadline)))
		timeout = timer.C
		defer timer.Stop()
	}
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	case <-timeout:
		return 0, os.ErrDeadlineExceeded
	}
}
func (c *shutdownConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	c.mu.Unlock()
	return c.Conn.SetWriteDeadline(t)
}
func (c *shutdownConn) Close() error { c.once.Do(func() { close(c.closed) }); return c.Conn.Close() }

func TestTLSShutdownDoesNotWaitForUnresponsivePeer(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	dial := &shutdownDialer{}
	a := accounts[0]
	a.Dialer = dial
	p, err := openMailPipe(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	dial.connection.blocked.Store(true)
	start := time.Now()
	p.close()
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("TLS teardown blocked for %s", elapsed)
	}
}

func TestEstablishedHTTPSOutlivesDialTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	server, err := NewIMAPLink(ctx, "lifetime", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "lifetime", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	payload := bytes.Repeat([]byte("stream survives its completed dial\n"), 8192)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(11 * time.Second):
			_, _ = w.Write(payload)
		case <-r.Context().Done():
		}
	}))
	defer origin.Close()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, server, "lifetime", key, func(dialCtx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(dialCtx, network, address)
			if err == nil {
				context.AfterFunc(dialCtx, func() { conn.Close() })
			}
			return conn, err
		}, nil)
	}()
	session, err := Connect(ctx, client, "lifetime", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	transport := origin.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	response, err := (&http.Client{Transport: transport, Timeout: 20 * time.Second}).Get(origin.URL)
	if err != nil {
		t.Fatal("established HTTPS died at dial deadline", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(body, payload) {
		t.Fatalf("HTTPS body after dial deadline: %v bytes=%d", err, len(body))
	}
	cancel()
	session.Close()
	client.Close()
	server.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("session shutdown did not join")
	}
}

// A provider can accept LOGIN but never finish a mailbox command. The client
// decoder replaces socket read deadlines while processing responses.
type slowSelection struct{ imapserver.Session }

func (s slowSelection) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	time.Sleep(500 * time.Millisecond)
	return s.Session.Select(name, options)
}
func TestMailboxCommandBoundSurvivesDecoderDeadlineChanges(t *testing.T) {
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return slowSelection{s} })
	pipe, err := openMailPipe(context.Background(), accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.close()
	stop := pipe.limit(75 * time.Millisecond)
	defer stop()
	start := time.Now()
	_, err = pipe.selectLog("bounded-mailbox", false)
	if err == nil || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("mailbox command exceeded independent bound: elapsed=%v err=%v", time.Since(start), err)
	}
}
