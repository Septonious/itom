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
	if order := l.idleInteractiveOrder(1, allowed, nil); len(order) != 4 || order[0] != 2 {
		t.Fatalf("loaded ranking %v", order)
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

func TestRC8LoadedTailRankingRetainsHealthyRotation(t *testing.T) {
	l := &IMAPLink{ctx: context.Background(), attached: SessionID{1}}
	l.lastBulk.Store(time.Now().UnixNano())
	var allowed [20]bool
	for i, ms := range []int{45, 50, 53, 48} {
		a := &mailAccountState{ready: true, controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}, interactive: make(chan mailWork, 16)}
		a.observeControlPublication(time.Duration(ms) * time.Millisecond)
		l.accounts = append(l.accounts, a)
		allowed[i] = true
	}
	for start := range l.accounts {
		if order := l.idleInteractiveOrder(start, allowed, nil); len(order) != 4 || order[0] != start {
			t.Fatalf("healthy rotation %d: %v", start, order)
		}
	}
	l.accounts[0].observeControlPublication(400 * time.Millisecond)
	if order := l.idleInteractiveOrder(0, allowed, nil); len(order) != 4 || order[0] == 0 || order[3] != 0 {
		t.Fatalf("stalled writer not deprioritized: %v", order)
	}
	// A loaded writer with tagged commands must lose to an idle peer.
	l.accounts[1].controlWriter.interactiveTags <- struct{}{}
	l.accounts[1].controlWriter.interactiveTags <- struct{}{}
	if order := l.idleInteractiveOrder(1, allowed, nil); len(order) != 4 || order[0] == 1 || order[0] == 0 {
		t.Fatalf("tag debt ignored: %v", order)
	}
	// A stale spike is not a permanent exclusion, and an explicit hedge
	// exclusion never becomes the preferred candidate.
	l.accounts[0].controlServiceAt.Store(time.Now().Add(-16 * time.Second).UnixNano())
	if order := l.idleInteractiveOrder(0, allowed, l.accounts[2]); len(order) != 4 || order[0] == 2 || order[3] != 2 {
		t.Fatalf("excluded writer preferred: %v", order)
	}
}

func TestRC8LoadedAdmissionSkipsRecentSlowPublisher(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, attached: id}
	l.lastBulk.Store(time.Now().UnixNano())
	for i, name := range []string{"first", "second", "third"} {
		a := &mailAccountState{link: l, config: MailAccount{ID: name}, id: id, ready: true, fast: make(chan mailWork, 8), interactive: make(chan mailWork, 8), controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}}
		a.observeControlPublication(45 * time.Millisecond)
		if i == 0 {
			a.observeControlPublication(400 * time.Millisecond)
		}
		l.accounts = append(l.accounts, a)
	}
	a, err := l.send(ctx, header(sealed, id, 1), true, nil)
	if err != nil || a != l.accounts[1] {
		t.Fatalf("loaded admission %v %v", a, err)
	}
	// No account is removed; even the slow one remains eligible for an
	// explicitly pinned same-account probe.
	if err := l.sendOnAccount(ctx, id, "first", header(sealed, id, 2), controlMessageLimit); err != nil {
		t.Fatal(err)
	}
	// Feedback stays on its selected account, independently of ranking.
	l.roundRobin = 0
	if err := l.SendFeedback(ctx, header(sealed, id, 3)); err != nil || len(l.accounts[0].fast) != 1 {
		t.Fatalf("feedback route changed: %v", err)
	}
}
