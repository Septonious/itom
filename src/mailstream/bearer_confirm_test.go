package mailstream

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type bearerTestLink struct{ peer atomic.Pointer[Session] }

func (l *bearerTestLink) Send(ctx context.Context, body []byte, _ bool) error {
	p := l.peer.Load()
	if p == nil {
		return ErrCapacity
	}
	select {
	case p.input <- bytes.Clone(body):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-p.ctx.Done():
		return ErrClosed
	}
}
func (*bearerTestLink) Receive() <-chan []byte                  { return nil }
func (*bearerTestLink) Attach(context.Context, SessionID) error { return nil }
func (*bearerTestLink) Close() error                            { return nil }

func TestRC23BearerConfirmedBeforeConcurrentOpens(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	key := make([]byte, 32)
	h, hello, err := initiate(key, "bearer-test")
	if err != nil {
		t.Fatal(err)
	}
	sc, reply, err := respond(key, "bearer-test", hello)
	if err != nil {
		t.Fatal(err)
	}
	cc, err := h.finish(reply)
	if err != nil {
		t.Fatal(err)
	}
	// The production READY and READY_ACK consume sequence zero before newSession.
	ready, _ := (frame{kind: frameReady}).encode()
	ack, _ := (frame{kind: frameReadyAck}).encode()
	if _, _, err := cc.seal(ready); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sc.seal(ack); err != nil {
		t.Fatal(err)
	}
	cl, sl := &bearerTestLink{}, &bearerTestLink{}
	observed := make(chan bool, 16)
	server := newSession(ctx, sl, sc, true, func(ctx context.Context, _, _ string) (net.Conn, error) {
		observed <- CellularExit(ctx)
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	})
	client := newSession(ctx, cl, cc, false, nil)
	cl.peer.Store(server)
	sl.peer.Store(client)
	defer server.Close()
	defer client.Close()
	for _, mobile := range []bool{true, false, true} {
		client.SetCellular(mobile)
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c, e := client.OpenTCP(ctx, "127.0.0.1:1234")
				if e == nil {
					c.Close()
				}
				errs <- e
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		for range 4 {
			select {
			case got := <-observed:
				if got != mobile {
					t.Fatalf("open used stale bearer: got %v want %v", got, mobile)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	client.mu.Lock()
	pings := client.nextPing
	client.mu.Unlock()
	if pings != 3 {
		t.Fatalf("expected one confirmation for each of three bearer epochs, got %d", pings)
	}
}
