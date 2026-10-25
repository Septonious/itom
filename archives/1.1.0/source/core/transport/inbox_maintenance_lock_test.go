package transport_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestQueuedPeerReceiptAndStatsProgressWhileFutureBodyTagIsHeld(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	body := serverBody(t, f, []byte("original authenticated peer message"))
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var writes sync.Mutex
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", transport.DefaultIMAPLimits(), "", true, func(c net.Conn) {
		r := bufio.NewReader(c)
		held := false
		reply := func(format string, args ...any) {
			writes.Lock()
			defer writes.Unlock()
			fmt.Fprintf(c, format, args...)
		}
		for {
			if _, err := r.Peek(1); err != nil {
				return
			}
			line := readLine(t, r)
			switch {
			case strings.Contains(line, "UID FETCH") && strings.HasSuffix(line, " (UID)\r\n"):
				reply("* 1 FETCH (UID 1)\r\n%s OK inventory\r\n", tag(line))
			case strings.Contains(line, "UID FETCH 1:1 (UID BODY.PEEK[])"):
				mime := body.MIME()
				reply("* 1 FETCH (UID 1 BODY[] {%d}\r\n%s)\r\n%s OK body\r\n", len(mime), mime, tag(line))
			case strings.Contains(line, "UID FETCH 2:2") && !held:
				held = true
				close(entered)
				go func(command string) { <-release; reply("%s OK empty future UID\r\n", tag(command)) }(line)
			case strings.Contains(line, "UID STORE 1 +FLAGS.SILENT (ITOM2R_"):
				reply("%s OK authenticated receipt marked\r\n", tag(line))
			case strings.Contains(line, "UID FETCH"):
				reply("%s OK empty\r\n", tag(line))
			default:
				t.Error("unexpected maintenance test command", line)
				return
			}
		}
	})
	defer x.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err = inbox.EnableDeliveryReceipts(); err != nil {
		t.Fatal(err)
	}
	if p, err := inbox.Sweep(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	held, err := f.store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx); close(done) }()
	defer func() { cancel(); releaseOnce.Do(func() { close(release) }); <-done }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("future BODY command did not start")
	}
	// The original future FETCH tag remains withheld. Neither the snapshot nor
	// the already authorized MAC STORE should need that unrelated tag to drain.
	stats := make(chan transport.DeliveryReceiptStats, 1)
	go func() { stats <- inbox.DeliveryReceiptStats() }()
	select {
	case <-stats:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("receipt snapshot waited on future BODY I/O")
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for inbox.DeliveryReceiptStats().Marked != 1 {
		select {
		case <-deadline.C:
			t.Fatal("queued authenticated receipt did not progress independently")
		case <-time.After(time.Millisecond):
		}
	}
	if now, err := f.store.Held("account1", "12345678"); err != nil || now != held {
		t.Fatal("receipt released original physical debt", held, now, err)
	}
	releaseOnce.Do(func() { close(release) })
}
