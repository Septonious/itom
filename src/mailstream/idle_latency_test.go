package mailstream

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

func TestRC7IdleSelectionUsesIndependentPublicationTail(t *testing.T) {
	l := &IMAPLink{ctx: context.Background(), attached: SessionID{1}}
	var allowed [20]bool
	for i, ms := range []int{30, 75, 45, 35} {
		a := &mailAccountState{ready: true, controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}, interactive: make(chan mailWork, 16)}
		a.observeControlPublication(time.Duration(ms) * time.Millisecond)
		l.accounts = append(l.accounts, a)
		allowed[i] = true
	}
	l.accounts[0].controlServiceJitter.Store(int64(50 * time.Millisecond))
	order := l.idleInteractiveOrder(1, allowed, nil)
	if len(order) != 4 || order[0] != 3 {
		t.Fatalf("tail ranking %v", order)
	}
	l.accounts[3].interactive <- mailWork{}
	order = l.idleInteractiveOrder(1, allowed, nil)
	if order[0] != 2 {
		t.Fatalf("queued path preferred %v", order)
	}
	if l.idleInteractiveOrder(0, allowed, nil) != nil {
		t.Fatal("round robin exploration disabled")
	}
	l.lastBulk.Store(time.Now().UnixNano())
	if l.idleInteractiveOrder(1, allowed, nil) != nil {
		t.Fatal("ranking under bulk")
	}
}
func TestRC7StandbyControlPollingRetainsFastGrantedPaths(t *testing.T) {
	l := &IMAPLink{grantSession: SessionID{1}, grant: map[string]bool{"active": true}}
	a := &mailAccountState{link: l, config: MailAccount{ID: "active"}}
	b := &mailAccountState{link: l, config: MailAccount{ID: "standby"}}
	if a.controlReadDelay() != time.Millisecond || b.controlReadDelay() != 40*time.Millisecond {
		t.Fatal("wrong polling delay")
	}
	l.grant["standby"] = true
	if b.controlReadDelay() != time.Millisecond {
		t.Fatal("new grant not promoted")
	}
	l.client = true
	delete(l.grant, "standby")
	if b.controlReadDelay() != time.Millisecond {
		t.Fatal("client polling changed")
	}
}
func TestRC7ControlEOFResumesSameAccountAndStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	accounts, _ := applicationMailServerAccounts(t, true, false, nil, nil, 3)
	srv, e := NewIMAPLink(ctx, "rc7-reader-recovery", accounts, false)
	if e != nil {
		t.Fatal(e)
	}
	defer srv.Close()
	cli, e := NewIMAPLink(ctx, "rc7-reader-recovery", accounts, true)
	if e != nil {
		t.Fatal(e)
	}
	defer cli.Close()
	key := bytes.Repeat([]byte{5}, 32)
	go Serve(ctx, srv, "rc7-reader-recovery", key, func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); _, _ = io.Copy(b, b) }()
		return a, nil
	}, nil)
	s, e := Connect(ctx, cli, "rc7-reader-recovery", key)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	waitRetention(t, ctx, func() bool { return cli.Snapshot().Data == 3 && srv.Snapshot().Data == 3 }, "initial attachment")
	c, e := s.OpenTCP(ctx, "example.com:443")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	echo := func() {
		t.Helper()
		c.SetDeadline(time.Now().Add(5 * time.Second))
		want := bytes.Repeat([]byte("read-resume"), 4096)
		sent := make(chan error, 1)
		go func() { _, e := c.Write(want); sent <- e }()
		got := make([]byte, len(want))
		if _, e := io.ReadFull(c, got); e != nil || !bytes.Equal(got, want) {
			t.Fatalf("echo %v", e)
		}
		if e := <-sent; e != nil {
			t.Fatal(e)
		}
	}
	echo()
	for _, l := range []*IMAPLink{cli, srv} {
		a := l.accounts[0]
		a.mu.Lock()
		old := a.control
		writer := a.controlWriter
		data := a.data
		id := a.id
		a.mu.Unlock()
		old.conn.NetConn().Close()
		waitRetention(t, ctx, func() bool { return a.controlReconnects.Load() == 1 && l.Snapshot().Data == 3 }, "control EOF recovery")
		a.mu.Lock()
		same := a.ready && !a.failed && a.control != old && a.controlWriter == writer && a.data == data && a.id == id
		a.mu.Unlock()
		if !same {
			t.Fatal("control reader recovery replaced unrelated roles")
		}
		echo()
	}
	if _, e = s.Ping(ctx); e != nil {
		t.Fatal(e)
	}
}
