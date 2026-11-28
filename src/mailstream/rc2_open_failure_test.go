package mailstream

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func TestRC2RemoteOpenFailureDoesNotKillSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	key := make([]byte, 32)
	h, hello, e := initiate(key, "open-failure")
	if e != nil {
		t.Fatal(e)
	}
	sc, reply, e := respond(key, "open-failure", hello)
	if e != nil {
		t.Fatal(e)
	}
	cc, e := h.finish(reply)
	if e != nil {
		t.Fatal(e)
	}
	ready, _ := (frame{kind: frameReady}).encode()
	ack, _ := (frame{kind: frameReadyAck}).encode()
	if _, _, e = cc.seal(ready); e != nil {
		t.Fatal(e)
	}
	if _, _, e = sc.seal(ack); e != nil {
		t.Fatal(e)
	}
	cl, sl := &bearerTestLink{}, &bearerTestLink{}
	cases := []struct {
		target string
		err    error
		reason OpenFailureReason
	}{
		{"policy.example:443", ErrExitPolicy, OpenFailurePolicy}, {"dns.example:443", ErrExitDNS, OpenFailureDNS}, {"connect.example:443", ErrExitConnect, OpenFailureConnect}, {"tls.example:443", ErrExitRelayTLS, OpenFailureRelayTLS}, {"dial.example:443", ErrExitRelayDial, OpenFailureRelayDial}, {"setup.example:443", ErrExitRelaySetup, OpenFailureRelaySetup}, {"cooldown.example:443", ErrExitRelayCooldown, OpenFailureRelayCooldown}, {"timeout.example:443", context.DeadlineExceeded, OpenFailureTimeout}, {"nil.example:443", nil, OpenFailureConnect},
	}
	server := newSession(ctx, sl, sc, true, func(_ context.Context, _, target string) (net.Conn, error) {
		for _, x := range cases {
			if target == x.target {
				return nil, x.err
			}
		}
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	})
	client := newSession(ctx, cl, cc, false, nil)
	cl.peer.Store(server)
	sl.peer.Store(client)
	defer func() { cancel(); client.Close(); server.Close() }()
	for _, x := range cases {
		c, e := client.OpenTCP(ctx, x.target)
		if c != nil {
			c.Close()
			t.Fatal("rejected target opened")
		}
		var failure RemoteOpenError
		if !errors.As(e, &failure) || failure.Reason != x.reason {
			t.Fatalf("%s: want reason %d got %v", x.target, x.reason, e)
		}
	}
	if _, e = client.Ping(ctx); e != nil {
		t.Fatal(e)
	}
	c, e := client.OpenTCP(ctx, "working.example:443")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	if _, e = c.Write([]byte("ok")); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 2)
	if _, e = io.ReadFull(c, buf); e != nil || string(buf) != "ok" {
		t.Fatal(e, string(buf))
	}
}

func TestRC2InvalidResetReasonRejected(t *testing.T) {
	s := &Session{server: true}
	if !errors.Is(s.apply(frame{kind: frameReset, stream: 1, offset: uint64(OpenFailureDNS)}), ErrProtocol) {
		t.Fatal("client forged reason accepted")
	}
	s.server = false
	if !errors.Is(s.apply(frame{kind: frameReset, stream: 1, offset: 999}), ErrProtocol) {
		t.Fatal("unknown reason accepted")
	}
}
