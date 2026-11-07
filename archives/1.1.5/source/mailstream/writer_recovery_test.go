package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// Real TLS/Noise/HTTPS: a bulk burst followed by a quiet writer and light
// application traffic. A provider EOF must replace only idle APPEND sockets;
// the authenticated session and both selected readers must survive.
func TestIdleWriterEOFDoesNotPermanentlyRetireAccount(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerAccounts(t, false, false, nil, nil, 1)
	server, err := NewStoredIMAPLink(ctx, "writer-recovery", accounts, false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewStoredIMAPLink(ctx, "writer-recovery", accounts, true, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := make([]byte, 32)
	rand.Read(key)
	go Serve(ctx, server, "writer-recovery", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "writer-recovery", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NewResponseController(w).EnableFullDuplex()
		w.Header().Set("Content-Type", "application/octet-stream")
		io.Copy(w, r.Body)
	}))
	defer origin.Close()
	transport := origin.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	transport.ForceAttemptHTTP2 = false
	httpClient := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	transfer := func(size int) {
		t.Helper()
		body := bytes.Repeat([]byte("v"), size)
		r, e := httpClient.Post(origin.URL, "application/octet-stream", bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		got, e := io.ReadAll(r.Body)
		r.Body.Close()
		if e != nil || !bytes.Equal(got, body) {
			t.Fatalf("HTTPS integrity after idle-writer loss: bytes=%d err=%v", len(got), e)
		}
	}
	transfer(4 << 20)
	// Stop transmitting so the provider EOF is unambiguous: no APPEND transaction
	// is deliberately interrupted and no unknown mail may be refunded.
	time.Sleep(500 * time.Millisecond)
	for _, link := range []*IMAPLink{client, server} {
		a := link.accounts[0]
		a.mu.Lock()
		control, data := a.control, a.data
		old, oldControl := a.writer, a.controlWriter
		spent, reserved, recovered := a.spent, a.reserved, a.recovered
		a.mu.Unlock()
		old.conn.NetConn().Close()
		oldControl.conn.NetConn().Close()
		until := time.Now().Add(3 * time.Second)
		restored := false
		for time.Now().Before(until) {
			a.mu.Lock()
			restored = !a.failed && a.writer != nil && a.controlWriter != nil && a.writer != old && a.controlWriter != oldControl && a.control == control && a.data == data
			a.mu.Unlock()
			if restored {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !restored {
			t.Fatalf("idle writer EOF permanently retired/stalled account: client=%v snapshot=%+v", link.client, link.Snapshot())
		}
		a.mu.Lock()
		debtPreserved := a.spent >= spent && a.reserved >= reserved && a.recovered == recovered
		a.mu.Unlock()
		if !debtPreserved {
			t.Fatal("writer replacement refunded unaudited mailbox debt")
		}
		transfer(256 << 10)
	}
}

// Blackhole the tagged response to the first actual idle-writer NOOP. This
// recreates the field failure through the server, without cutting active DATA
// APPENDs or advancing a fake clock. LOGIN and APPEND replies remain normal.
type idleNoopStall struct {
	imapserver.Session
	armed                          *atomic.Bool
	stalled                        *atomic.Int32
	seenAppend, appended, selected bool
}

func (s *idleNoopStall) Select(name string, options *imap.SelectOptions) (*imap.SelectData, error) {
	d, e := s.Session.Select(name, options)
	if e == nil {
		s.selected = true
	}
	return d, e
}
func (s *idleNoopStall) Append(name string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	s.seenAppend, s.appended = true, true
	return s.Session.Append(name, r, options)
}
func (s *idleNoopStall) Poll(w *imapserver.UpdateWriter, allow bool) error {
	appended := s.appended
	s.appended = false
	if !s.selected && s.seenAppend && !appended && s.armed.Load() && s.stalled.CompareAndSwap(0, 1) {
		time.Sleep(7 * time.Second)
		return io.ErrUnexpectedEOF
	}
	return s.Session.Poll(w, allow)
}
func TestWriterKeepaliveTimeoutDuringVideoRecovers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Second)
	defer cancel()
	var armed atomic.Bool
	var stalled atomic.Int32
	accounts, _ := applicationMailServerAccounts(t, false, false, func(s imapserver.Session) imapserver.Session {
		return &idleNoopStall{Session: s, armed: &armed, stalled: &stalled}
	}, nil, 1)
	server, err := NewIMAPLink(ctx, "video-keepalive", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "video-keepalive", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	key := make([]byte, 32)
	rand.Read(key)
	go Serve(ctx, server, "video-keepalive", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, client, "video-keepalive", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	payload := bytes.Repeat([]byte("video"), 13107)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			http.NewResponseController(w).EnableFullDuplex()
			io.Copy(w, r.Body)
			return
		}
		w.Write(payload)
	}))
	defer origin.Close()
	transport := origin.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, _, target string) (net.Conn, error) { return session.OpenTCP(ctx, target) }
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	burst := bytes.Repeat([]byte("bulk"), 1<<20)
	res, err := httpClient.Post(origin.URL, "application/octet-stream", bytes.NewReader(burst))
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || !bytes.Equal(got, burst) {
		t.Fatal("bulk integrity", err)
	}
	armed.Store(true)
	a := client.accounts[0]
	a.mu.Lock()
	control, data := a.control, a.data
	a.mu.Unlock()
	duration := 75 * time.Second
	if os.Getenv("ITOM_VIDEO_SOAK") == "1" {
		duration = 185 * time.Second
	}
	started := time.Now()
	until := started.Add(duration)
	recoveredAt := time.Time{}
	segments := 0
	for time.Now().Before(until) {
		res, err = httpClient.Get(origin.URL)
		if err != nil {
			t.Fatal("video request after burst", err)
		}
		got, err = io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatal("video segment integrity", err, len(got))
		}
		segments++
		a.mu.Lock()
		failed, reconnects, sameReaders := a.failed, a.writerReconnects, a.control == control && a.data == data
		a.mu.Unlock()
		if failed || !sameReaders {
			t.Fatal("keepalive retired account or reset selected readers", client.Snapshot())
		}
		if reconnects > 0 && recoveredAt.IsZero() {
			recoveredAt = time.Now()
		}
		if !recoveredAt.IsZero() && time.Since(recoveredAt) > 5*time.Second && (os.Getenv("ITOM_VIDEO_SOAK") != "1" || time.Since(started) > 180*time.Second) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if stalled.Load() != 1 || recoveredAt.IsZero() {
		t.Fatalf("real keepalive timeout was not repaired: stalled=%d snapshot=%+v", stalled.Load(), client.Snapshot())
	}
	if m := client.Snapshot(); m.Failed != 0 || m.Writers != 1 || m.ControlWriters != 1 || m.Data != 1 {
		t.Fatal("four lines not restored", m)
	}
	t.Logf("bulk burst then %d verified video segments; actual NOOP blackhole bounded and writers restored without reader/session reset", segments)
}
