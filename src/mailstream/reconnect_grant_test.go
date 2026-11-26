package mailstream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// A retrying account never reaches the APPEND actor loop. An unallocated
// account in this state must not stop the standing server from accepting the
// next authenticated session, even when it precedes all allocated accounts.
type unavailableReconnectDialer struct{}

func (unavailableReconnectDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("fixture account temporarily unavailable")
}

func TestReconnectWithUnallocatedStartupRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 15)
	accounts[0].Dialer = unavailableReconnectDialer{}
	server, err := NewIMAPLink(ctx, "reconnect-grant", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = accounts[i+1].ID
	}
	peers := make(chan *Session, 3)
	serverDone := make(chan error, 1)
	key := bytes.Repeat([]byte{1}, 32)
	go func() {
		serverDone <- ServeWithOptions(ctx, server, "reconnect-grant", key, func(context.Context, string, string) (net.Conn, error) {
			a, b := net.Pipe()
			go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
			return a, nil
		}, ServerOptions{InitialAccountGrant: func() []string { return ids }, LiveAccountGrant: func() []string { return ids }, Connected: func(s *Session) { peers <- s }})
	}()
	for round := 0; round < 3; round++ {
		client, e := NewIMAPLink(ctx, "reconnect-grant", accounts, true)
		if e != nil {
			t.Fatal(e)
		}
		func() {
			defer client.Close()
			startup, stop := context.WithTimeout(ctx, 5*time.Second)
			s, e := Connect(startup, client, "reconnect-grant", key)
			stop()
			if e != nil {
				t.Fatalf("connection %d: %v", round+1, e)
			}
			defer s.Close()
			var peer *Session
			select {
			case peer = <-peers:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			deadline := time.Now().Add(4 * time.Second)
			for {
				m := server.Snapshot()
				c := client.Snapshot()
				if m.Data == 8 && m.ControlWriters == 8 && c.Data == 8 && c.ControlWriters == 8 && m.Ready == 14 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("connection %d roles: server %d/%d/%d client %d/%d", round+1, m.Ready, m.Data, m.ControlWriters, c.Data, c.ControlWriters)
				}
				time.Sleep(20 * time.Millisecond)
			}
			stream, e := s.OpenTCP(ctx, "example.com:443")
			if e != nil {
				t.Fatal(e)
			}
			stream.SetDeadline(time.Now().Add(3 * time.Second))
			want := bytes.Repeat([]byte("reconnect-payload"), 2048)
			if _, e = stream.Write(want); e != nil {
				t.Fatal(e)
			}
			got := make([]byte, len(want))
			if _, e = io.ReadFull(stream, got); e != nil || !bytes.Equal(want, got) {
				t.Fatalf("echo %d: %v", round+1, e)
			}
			stream.Close()
			s.Close()
			client.Close()
			// Model the server's ordinary idle/session-ended path, independently of
			// the Android client's ability to deliver a final close packet.
			peer.fail(ErrClosed)
			deadline = time.Now().Add(2 * time.Second)
			for {
				m := server.Snapshot()
				if !m.GrantNegotiated && m.Data == 0 && m.Writers == 0 && m.ControlWriters == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("connection %d teardown stalled: grant=%v data=%d writers=%d control=%d", round+1, m.GrantNegotiated, m.Data, m.Writers, m.ControlWriters)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
	}
	cancel()
	server.Close()
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}
}
