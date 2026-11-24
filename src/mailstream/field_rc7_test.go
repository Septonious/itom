package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// Speedtest MULTI plus Android background sockets reaches 64 before any
// transfer fails. Keep the receive-memory bound; wait for a slot instead of
// failing the next TLS connection immediately. A cancelled waiter must leave
// neither a stream nor an OPEN packet behind.
func TestBurstOpenWaitsForCapacityAndHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{ctx: ctx, cancel: cancel, streams: map[uint32]*Stream{}, closedStreams: map[uint32]*terminalStream{}, nextStream: 129, changed: make(chan struct{}), wake: make(chan struct{}, 1)}
	for id := uint32(1); id < 129; id += 2 {
		s.streams[id] = newStream(s, id)
	}
	wait, stop := context.WithTimeout(ctx, 60*time.Millisecond)
	defer stop()
	start := time.Now()
	_, err := s.OpenTCP(wait, "127.0.0.1:443")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) < 40*time.Millisecond {
		t.Fatalf("burst OPEN was rejected instead of waiting: %v elapsed=%s", err, time.Since(start))
	}
	if len(s.streams) != maxStreams || s.nextStream != 129 || len(s.pending) != 0 {
		t.Fatal("cancelled admission consumed a stream or published an OPEN")
	}
}

// A MULTI run starts while Android still has 64 established background sockets.
// Sixteen new connections must resume as old connections close, with the same
// session and verified echo bytes. No SINGLE run is used to warm the transport.
func TestMultiBurstAfterFullBackgroundStreamSet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	server, err := NewIMAPLink(ctx, "multi-burst", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "multi-burst", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	go Serve(ctx, server, "multi-burst", key, (&net.Dialer{}).DialContext, nil)
	s, err := Connect(ctx, client, "multi-burst", key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var background []net.Conn
	defer func() {
		for _, c := range background {
			c.Close()
		}
	}()
	for range maxStreams {
		c, e := s.OpenTCP(ctx, listener.Addr().String())
		if e != nil {
			t.Fatalf("background open %d: %v", len(background), e)
		}
		background = append(background, c)
	}
	type result struct {
		c   net.Conn
		err error
	}
	results := make(chan result, 16)
	for range 16 {
		go func() { c, e := s.OpenTCP(ctx, listener.Addr().String()); results <- result{c, e} }()
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.Snapshot().OpenWaits < 16 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := s.Snapshot(); snapshot.OpenWaits != 16 || snapshot.OpenRefusals != 0 || snapshot.Streams != 64 {
		t.Fatalf("burst did not wait within existing memory bound: %+v", snapshot)
	}
	for _, c := range background[:16] {
		c.Close()
	}
	payload := []byte("MULTI works before SINGLE")
	for range 16 {
		select {
		case r := <-results:
			if r.err != nil {
				t.Fatal("queued burst OPEN", r.err)
			}
			r.c.SetDeadline(time.Now().Add(4 * time.Second))
			if _, err = r.c.Write(payload); err != nil {
				r.c.Close()
				t.Fatal(err)
			}
			got := make([]byte, len(payload))
			_, err = io.ReadFull(r.c, got)
			r.c.Close()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("burst integrity: %q %v", got, err)
			}
		case <-ctx.Done():
			t.Fatal("burst never resumed", s.Snapshot())
		}
	}
	if s.Snapshot().OpenRefusals != 0 || s.Snapshot().Closed {
		t.Fatal("burst damaged session", s.Snapshot())
	}
	t.Log("64 background sockets, 16 queued opens, 16 verified echoes, no capacity refusals")
}
