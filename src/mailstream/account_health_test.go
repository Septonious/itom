package mailstream

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// Permanently lost local control readers must be reported through surviving
// paths. Replace both at once; verify real DATA echo and all eight physical
// account sets without recreating the stream or session.
func TestRC7ReplaceTwoFailedAccountsInLiveSession(t *testing.T) {
	testRC7Repair(t, false)
}
func TestRC7FailedReplacementDoesNotWedgeGrant(t *testing.T) {
	testRC7Repair(t, true)
}
func testRC7Repair(t *testing.T, failReplacement bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 15)
	srv, e := NewIMAPLink(ctx, "rc7-repair", accounts, false)
	if e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	cli, e := NewIMAPLink(ctx, "rc7-repair", accounts, true)
	if e != nil {
		t.Fatal(e)
	}
	defer cli.Close()
	ids := make([]string, 8)
	for i := range ids {
		ids[i] = accounts[i].ID
	}
	var desired atomic.Pointer[[]string]
	desired.Store(&ids)
	key := bytes.Repeat([]byte{7}, 32)
	go ServeWithOptions(ctx, srv, "rc7-repair", key, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	}, ServerOptions{InitialAccountGrant: func() []string { return ids }, LiveAccountGrant: func() []string { return *desired.Load() }})
	s, e := Connect(ctx, cli, "rc7-repair", key)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	await := func(timeout time.Duration, fn func() bool) {
		t.Helper()
		d := time.Now().Add(timeout)
		for !fn() {
			if time.Now().After(d) {
				t.Fatalf("timeout client %d/%d, server %d/%d, session %v", cli.Snapshot().Data, cli.Snapshot().AllocatedAccounts, srv.Snapshot().Data, srv.Snapshot().AllocatedAccounts, s.Err())
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	await(8*time.Second, func() bool { return cli.Snapshot().Data == 8 && srv.Snapshot().Data == 8 })
	stream, e := s.OpenTCP(ctx, "example.com:443")
	if e != nil {
		t.Fatal(e)
	}
	defer stream.Close()
	echo := func() {
		t.Helper()
		b := bytes.Repeat([]byte("rc7-data!"), 8192)
		stream.SetDeadline(time.Now().Add(6 * time.Second))
		sent := make(chan error, 1)
		go func() { _, e := stream.Write(b); sent <- e }()
		got := make([]byte, len(b))
		if _, e := io.ReadFull(stream, got); e != nil || !bytes.Equal(got, b) {
			t.Fatalf("echo %v", e)
		}
		if e := <-sent; e != nil {
			t.Fatal(e)
		}
	}
	echo()
	cli.account(ids[0]).fail()
	srv.account(ids[4]).fail()
	await(8*time.Second, func() bool {
		for _, a := range srv.Snapshot().Lines {
			if a.Account == ids[0] {
				return a.PeerUnavailable
			}
		}
		return false
	})
	replacement := append([]string(nil), ids...)
	replacement[0] = accounts[8].ID
	replacement[4] = accounts[9].ID
	if failReplacement {
		cli.account(accounts[8].ID).fail()
		desired.Store(&replacement)
		await(12*time.Second, func() bool {
			m := srv.Snapshot()
			if !m.GrantChanging {
				return false
			}
			for _, a := range m.Lines {
				if a.Account == accounts[8].ID {
					return a.PeerUnavailable
				}
			}
			return false
		})
		replacement = append([]string(nil), replacement...)
		replacement[0] = accounts[10].ID
	}
	desired.Store(&replacement)
	await(32*time.Second, func() bool {
		m := cli.Snapshot()
		if m.Data != 8 || m.Ready != 8 || m.AllocatedAccounts != 8 || m.Writers != 8 || m.ControlWriters != 8 || m.GrantChanging {
			return false
		}
		for _, a := range m.Lines {
			if a.Allocated && (a.Account == ids[0] || a.Account == ids[4]) {
				return false
			}
		}
		return true
	})
	echo()
	if _, e = s.Ping(ctx); e != nil {
		t.Fatal(e)
	}
}
