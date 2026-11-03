package mailstream

import (
	"context"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"
)

// The origin consumes a request FIN but never sends a response FIN. Session
// replacement must close its established exit socket, not wait for that peer.
func TestSessionReplacementAfterHalfCloseAndSilentOrigin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	server, err := NewIMAPLink(ctx, "half-close-restart", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	origin := make(chan net.Conn, 1)
	received := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			received <- e
			return
		}
		origin <- c
		_, e = io.ReadAll(c)
		received <- e
	}()
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	sessions := make(chan *Session, 2)
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, server, "half-close-restart", key, (&net.Dialer{}).DialContext, func(s *Session) { sessions <- s })
	}()
	client, err := NewIMAPLink(ctx, "half-close-restart", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	session, err := Connect(ctx, client, "half-close-restart", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	peer := <-sessions
	stream, err := session.OpenTCP(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	conn := <-origin
	defer conn.Close()
	if _, err = stream.Write([]byte("request then FIN")); err != nil {
		t.Fatal(err)
	}
	if err = stream.(*Stream).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err = <-received; err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { peer.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		conn.Close()
		<-closed
		t.Fatal("session close waited for the half-closed origin; replacement is blocked")
	}
	session.Close()
	client.Close()
	next, err := NewIMAPLink(ctx, "half-close-restart", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	startup, stop := context.WithTimeout(ctx, 3*time.Second)
	replacement, err := Connect(startup, next, "half-close-restart", key)
	stop()
	if err != nil {
		t.Fatal("fresh session after abandoned half-close:", err)
	}
	replacement.Close()
	next.Close()
	cancel()
	server.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server did not join")
	}
}
