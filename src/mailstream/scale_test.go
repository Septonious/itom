package mailstream

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// Exercise authenticated grants through the real TLS IMAP carrier, not just
// a planner or a displayed count. Unallocated bootstrap sockets must exit.
func TestLiveAccountScalingPreservesStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 15)
	server, err := NewIMAPLink(ctx, "scaletest", accounts, false)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := NewIMAPLink(ctx, "scaletest", accounts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	grant := make([]string, 8)
	for i := range grant {
		grant[i] = accounts[i+7].ID
	}
	key := bytes.Repeat([]byte{1}, 32)
	go ServeWithOptions(ctx, server, "scaletest", key, func(ctx context.Context, network, address string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	}, ServerOptions{InitialAccountGrant: func() []string { return grant }})
	session, err := Connect(ctx, client, "scaletest", key)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, s := client.Snapshot(), server.Snapshot()
		if c.Ready == 8 && c.Data == 8 && c.Writers == 8 && c.ControlWriters == 8 && s.Ready == 15 && s.Data == 8 && s.Writers == 8 && s.ControlWriters == 8 {
			if !c.GrantNegotiated || c.AllocatedAccounts != 8 || c.Failed != 0 || !s.GrantNegotiated || s.AllocatedAccounts != 8 {
				t.Fatalf("invalid grant counters: client=%+v server=%+v", c, s)
			}
			for i, line := range c.Lines {
				if i < 7 && (line.Allocated || line.Ready || line.Data || line.Writer || line.ControlWriter || line.Stage != "unallocated") {
					t.Fatalf("unallocated account still active: %+v", line)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("grant not applied: client roles=%d/%d/%d/%d server roles=%d/%d/%d/%d", c.Ready, c.Data, c.Writers, c.ControlWriters, s.Ready, s.Data, s.Writers, s.ControlWriters)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	if _, err = session.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	stream, err := session.OpenTCP(ctx, "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(35 * time.Second))
	want := []byte("grant-path-check")
	if _, err = stream.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err = io.ReadFull(stream, got); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("echo %q %v", got, err)
	}
	transfer := func(n int) {
		t.Helper()
		payload := bytes.Repeat([]byte("0123456789abcdef"), n/16)
		errc := make(chan error, 1)
		go func() { _, e := stream.Write(payload); errc <- e }()
		actual := make([]byte, len(payload))
		if _, err := io.ReadFull(stream, actual); err != nil || !bytes.Equal(actual, payload) {
			t.Fatalf("data %d %v", n, err)
		}
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	transfer(2 << 20)
	for _, target := range [][]string{grant[:1], grant, grant[:2], grant} {
		resize, stop := context.WithTimeout(ctx, 10*time.Second)
		if err := session.SetActiveAccounts(resize, target); err != nil {
			stop()
			t.Fatalf("resize %d: %v, state %+v", len(target), err, client.Snapshot())
		}
		stop()
		snap := client.Snapshot()
		if snap.Ready != len(target) || snap.Data != len(target) || snap.Writers != len(target) || snap.ControlWriters != len(target) || snap.AllocatedAccounts != 8 || snap.Failed != 0 {
			t.Fatalf("wrong resize %d: %+v", len(target), snap)
		}
		t.Logf("resized to %d accounts / %d sockets", snap.Ready, snap.Ready+snap.Data+snap.Writers+snap.ControlWriters)
		transfer(512 << 10)
		if _, err := session.Ping(ctx); err != nil {
			t.Fatal(err)
		}
	}

}
