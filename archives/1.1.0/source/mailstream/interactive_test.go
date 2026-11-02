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
	"sync/atomic"
	"testing"
	"time"
)

// Delay DATA publication while allowing the real TLS IMAP control path to run.
// Accepted messages remain immutable in this finite fixture queue. This models
// bulk already in flight when a measured delivery window shrinks.
type delayedBulkLink struct {
	*IMAPLink
	data    chan []byte
	release chan struct{}
	hold    atomic.Bool
}

func (l *delayedBulkLink) Send(ctx context.Context, wire []byte, priority bool) error {
	if priority || !l.hold.Load() {
		return l.IMAPLink.Send(ctx, wire, true)
	}
	select {
	case l.data <- bytes.Clone(wire):
		return nil
	default:
		return ErrCapacity
	}
}
func (l *delayedBulkLink) forward(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-l.release:
	}
	for {
		select {
		case <-ctx.Done():
			return
		case wire := <-l.data:
			for {
				err := l.IMAPLink.Send(ctx, wire, false)
				if !errors.Is(err, ErrCapacity) {
					break
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Millisecond):
				}
			}
		}
	}
}

func TestFreshHTTPSWhileBulkWindowShrinks(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	server, err := NewIMAPLink(ctx, "interactive", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "interactive", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gate := &delayedBulkLink{IMAPLink: client, data: make(chan []byte, 64), release: make(chan struct{})}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	go Serve(ctx, server, "interactive", key, (&net.Dialer{}).DialContext, nil)
	session, err := Connect(ctx, gate, "interactive", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	payload := bytes.Repeat([]byte{0x5a}, 512<<10)
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, e := io.ReadAll(r.Body)
			if e != nil || !bytes.Equal(body, payload) {
				http.Error(w, "payload integrity", 500)
				return
			}
			io.WriteString(w, "uploaded")
			return
		}
		io.WriteString(w, "fresh HTTPS")
	}))
	transport := origin.Client().Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) { return session.OpenTCP(ctx, address) }
	defer func() { cancel(); transport.CloseIdleConnections(); origin.Close() }()
	gate.hold.Store(true)
	go gate.forward(ctx)
	session.mu.Lock()
	session.delivery.limit = 2 << 20
	session.mu.Unlock()
	results := make(chan error, 8)
	for range 8 {
		go func() {
			request, e := http.NewRequestWithContext(ctx, http.MethodPost, origin.URL, bytes.NewReader(payload))
			if e != nil {
				results <- e
				return
			}
			response, e := (&http.Client{Transport: transport}).Do(request)
			if e == nil {
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				e = readErr
				if e == nil && string(body) != "uploaded" {
					e = errors.New("bulk HTTPS integrity")
				}
			}
			results <- e
		}()
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		session.mu.Lock()
		pressured := session.delivery.flight > initialDeliveryWindow && session.pendingBytes > 1<<20
		if pressured {
			session.delivery.limit = initialDeliveryWindow
			// Keep the injected low bulk estimate fixed while the DATA path is held.
			session.delivery.start = time.Now().Add(time.Hour)
		} else {
			session.delivery.limit = 2 << 20
		}
		session.mu.Unlock()
		if pressured {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not create real bulk flight: %+v", session.Snapshot())
		}
		time.Sleep(time.Millisecond)
	}
	get := func() {
		response, e := (&http.Client{Transport: transport, Timeout: 2 * time.Second}).Get(origin.URL)
		if e != nil {
			t.Fatal("fresh HTTPS starved behind old bulk flight:", e)
		}
		body, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || string(body) != "fresh HTTPS" {
			t.Fatalf("fresh HTTPS response: %q %v", body, e)
		}
	}
	t.Logf("before fresh HTTPS: %+v", session.Snapshot())
	get()
	t.Logf("after fresh HTTPS: %+v", session.Snapshot())
	session.mu.Lock()
	session.delivery.start = time.Time{}
	session.mu.Unlock()
	close(gate.release)
	for range 8 {
		if e := <-results; e != nil {
			t.Fatal("bulk recovery:", e)
		}
	}
	get()
	t.Logf("fresh TLS/HTTPS completed while bulk exceeded its shrunken window; recovered %d verified upload bytes", 8*len(payload))
}

// The latency escape must remain finite and cannot admit large DATA or consume
// the protocol frames needed to release existing application credit.
func TestInteractiveReserveIsBoundedAndReturnsOnStreamCancellation(t *testing.T) {
	key := make([]byte, 32)
	h, hello, err := initiate(key, "reserve")
	if err != nil {
		t.Fatal(err)
	}
	_, reply, err := respond(key, "reserve", hello)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := h.finish(reply)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{ctx: context.Background(), cipher: cipher, pending: map[uint64]*pendingPacket{}, closedStreams: map[uint32]*terminalStream{}, changed: make(chan struct{}), wake: make(chan struct{}, 1)}
	for {
		err = s.trySend(frame{kind: frameData, stream: 1, data: make([]byte, 32<<10)}, false)
		if errors.Is(err, ErrCapacity) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < controlReserve; i++ {
		if err = s.trySend(frame{kind: frameData, stream: 3, data: make([]byte, 4096)}, true); err != nil {
			t.Fatalf("interactive reserve %d: %v", i, err)
		}
	}
	if err = s.trySend(frame{kind: frameData, stream: 3, data: []byte("overflow")}, true); !errors.Is(err, ErrCapacity) {
		t.Fatal("interactive queue is not bounded", err)
	}
	if err = s.trySend(frame{kind: frameData, stream: 5, data: make([]byte, 4097)}, true); !errors.Is(err, ErrCapacity) {
		t.Fatal("large DATA bypassed bulk admission", err)
	}
	if err = s.trySend(frame{kind: frameReset, stream: 7}, true); err != nil {
		t.Fatal("protocol reserve was consumed", err)
	}
	s.abandonStream(3)
	if err = s.trySend(frame{kind: frameData, stream: 9, data: []byte("after cancellation")}, true); err != nil {
		t.Fatal("cancellation leaked the interactive reserve", err)
	}
}
