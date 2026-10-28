package transport

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// A destructive mailbox command owns an entire command-completion interval,
// not just the read-admission queue. APPEND stays pipelined with ordinary FETCH
// outside that interval, but cannot cross an outstanding EXPUNGE tag.
func TestMailboxExpungeDrainsAllTagsAndFencesLaterCommands(t *testing.T) {
	x, peer := standbyPeer(t)
	x.uidPlus, x.literalPlus, x.mailbox, x.validity = true, true, "fast", 7
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := bufio.NewReader(peer)
	type issued struct {
		c   *Command
		err error
	}
	firstResult, eraseResult := make(chan issued, 1), make(chan issued, 1)
	go func() { c, e := x.fetchInbox(ctx, 1); firstResult <- issued{c, e} }()
	first, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	fetch := <-firstResult
	if fetch.err != nil {
		t.Fatal(fetch.err)
	}
	go func() { c, e := x.deleteUIDSet(ctx, []uint32{2}, true); eraseResult <- issued{c, e} }()
	quiet := func(boundary string) {
		t.Helper()
		peer.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
		line, e := r.ReadString('\n')
		peer.SetReadDeadline(time.Time{})
		var timeout net.Error
		if errors.As(e, &timeout) && timeout.Timeout() {
			return
		}
		// Drain an early APPEND literal before the modeled provider closes, so
		// the retained first failure describes the outstanding command interval.
		if strings.Contains(line, " APPEND ") {
			b := make([]byte, 6)
			_, _ = io.ReadFull(r, b)
		}
		peer.Close()
		select {
		case <-x.done:
		case <-time.After(time.Second):
		}
		snapshot := x.Snapshot()
		t.Fatalf("%s: unsafe command %q (%v), close=%s commands=%v", boundary, line, e, snapshot.CloseReason, snapshot.CloseCommands)
	}
	quiet("FETCH tag must complete first")
	fmt.Fprintf(peer, "* 1 FETCH (UID 1 BODY[] {1}\r\nx)\r\n%s OK fetched\r\n", strings.Fields(first)[0])
	if _, err = fetch.c.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	erase, err := r.ReadString('\n')
	if err != nil || !strings.Contains(erase, " UID EXPUNGE 2\r\n") {
		t.Fatal(erase, err)
	}
	expunge := <-eraseResult
	if expunge.err != nil {
		t.Fatal(expunge.err)
	}
	detached, stop := context.WithCancel(ctx)
	stop()
	if _, err = expunge.c.Wait(detached); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	later := make(chan issued, 2)
	go func() { c, e := x.fetchInbox(ctx, 3); later <- issued{c, e} }()
	go func() { c, e := x.Append(ctx, "fast", []byte("bulk")); later <- issued{c, e} }()
	quiet("detached EXPUNGE still fences APPEND and FETCH")
	fmt.Fprintf(peer, "* 2 EXPUNGE\r\n%s OK erased\r\n", strings.Fields(erase)[0])
	if _, err = expunge.c.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		line, e := r.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		switch {
		case strings.Contains(line, " APPEND \"fast\" {4+}\r\n"):
			b := make([]byte, 6)
			if _, e = io.ReadFull(r, b); e != nil || string(b) != "bulk\r\n" {
				t.Fatal(string(b), e)
			}
			fmt.Fprintf(peer, "%s OK appended\r\n", strings.Fields(line)[0])
		case strings.Contains(line, " UID FETCH 3:3 "):
			fmt.Fprintf(peer, "* 2 FETCH (UID 3 BODY[] {1}\r\ny)\r\n%s OK fetched\r\n", strings.Fields(line)[0])
		default:
			t.Fatal("unexpected post-fence command", line)
		}
	}
	for range 2 {
		v := <-later
		if v.err != nil {
			t.Fatal(v.err)
		}
		if _, err = v.c.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if s := x.Snapshot(); s.Closed || s.PendingTags != 0 || s.Expunge.Completed != 1 || s.Append.Completed != 1 {
		t.Fatal(s)
	}
}

func TestCanceledExpungeAdmissionDoesNotIssueDeleteOrAbandonPriorFetch(t *testing.T) {
	x, peer := standbyPeer(t)
	x.uidPlus, x.mailbox, x.validity = true, "fast", 7
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := bufio.NewReader(peer)
	first := make(chan *Command, 1)
	go func() { c, _ := x.fetchInbox(ctx, 1); first <- c }()
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	fetch := <-first
	admission, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { _, err := x.deleteUIDSet(admission, []uint32{2}, true); result <- err }()
	stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fmt.Fprintf(peer, "* 1 FETCH (UID 1 BODY[] {1}\r\nx)\r\n%s OK fetched\r\n", strings.Fields(line)[0])
	if _, err := fetch.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	later := make(chan error, 1)
	go func() {
		c, e := x.fetchInbox(ctx, 3)
		if e == nil {
			_, e = c.Wait(ctx)
		}
		later <- e
	}()
	line, err = r.ReadString('\n')
	if err != nil || !strings.Contains(line, " UID FETCH 3:3 ") {
		t.Fatal("canceled deletion escaped admission or retained writer", line, err)
	}
	fmt.Fprintf(peer, "* 2 FETCH (UID 3 BODY[] {1}\r\ny)\r\n%s OK fetched\r\n", strings.Fields(line)[0])
	if err := <-later; err != nil {
		t.Fatal(err)
	}
	if x.Snapshot().Expunge.Started != 0 {
		t.Fatal("canceled admission issued EXPUNGE")
	}
}

func TestClosingExecutorJoinsDetachedExclusiveExpungeLease(t *testing.T) {
	x, peer := standbyPeer(t)
	x.uidPlus, x.mailbox, x.validity = true, "fast", 7
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	issued := make(chan *Command, 1)
	go func() { c, _ := x.deleteUIDSet(ctx, []uint32{2}, true); issued <- c }()
	line, err := bufio.NewReader(peer).ReadString('\n')
	if err != nil || !strings.Contains(line, " UID EXPUNGE 2") {
		t.Fatal(line, err)
	}
	command := <-issued
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err = command.Wait(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- x.Close() }()
	select {
	case err = <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("Close did not join detached wire lease")
	}
	if len(x.writer) != 0 || x.Snapshot().PendingTags != 0 {
		t.Fatal("wire lease survived Close", x.Snapshot())
	}
	if _, err = command.Wait(ctx); err == nil {
		t.Fatal("owner-close fabricated tagged completion")
	}
}
