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

// A provider may not interleave mailbox erasure with an outstanding FETCH.
// Exercise actual wire admission, including an empty interval before each tag;
// the mere absence of an active literal is not a safe handoff boundary.
func TestMailboxExpungeDrainsFetchTagsAndFencesLaterReadsButNotAppend(t *testing.T) {
	x, peer := standbyPeer(t)
	x.uidPlus, x.literalPlus, x.mailbox, x.validity = true, true, "fast", 7
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := bufio.NewReader(peer)
	commands := make(chan *Command, 3)
	errs := make(chan error, 3)
	go func() { c, err := x.fetchInbox(ctx, 1); commands <- c; errs <- err }()
	first, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	fetch := <-commands
	if err = <-errs; err != nil {
		t.Fatal(err)
	}
	go func() { c, err := x.deleteUIDSet(ctx, []uint32{2}, true); commands <- c; errs <- err }()
	assertQuiet := func(boundary string) {
		t.Helper()
		peer.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
		line, e := r.ReadString('\n')
		peer.SetReadDeadline(time.Time{})
		var timeout net.Error
		if !errors.As(e, &timeout) || !timeout.Timeout() {
			peer.Close() // observed field failure category: peer EOF during FETCH/EXPUNGE
			t.Fatalf("%s: provider saw unsafe command before completion tag: %q (%v)", boundary, line, e)
		}
	}
	assertQuiet("FETCH owns mailbox sequence state")
	fmt.Fprintf(peer, "* 1 FETCH (UID 1 BODY[] {1}\r\nx)\r\n%s OK fetched\r\n", strings.Fields(first)[0])
	if _, err = fetch.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	erase, err := r.ReadString('\n')
	if err != nil || !strings.Contains(erase, " UID EXPUNGE 2\r\n") {
		t.Fatal(erase, err)
	}
	expunge := <-commands
	if err = <-errs; err != nil {
		t.Fatal(err)
	}
	detached, stop := context.WithCancel(ctx)
	stop()
	if _, err = expunge.Wait(detached); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	go func() { c, err := x.fetchInbox(ctx, 3); commands <- c; errs <- err }()
	go func() { c, err := x.Append(ctx, "fast", []byte("bulk")); commands <- c; errs <- err }()
	appendLine, err := r.ReadString('\n')
	if err != nil || !strings.Contains(appendLine, " APPEND \"fast\" {4+}\r\n") {
		t.Fatal("APPEND should remain concurrent; later FETCH must wait", appendLine, err)
	}
	literal := make([]byte, 6)
	if _, err = io.ReadFull(r, literal); err != nil || string(literal) != "bulk\r\n" {
		t.Fatal(string(literal), err)
	}
	appended := <-commands
	if err = <-errs; err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(peer, "%s OK appended\r\n", strings.Fields(appendLine)[0])
	if _, err = appended.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	assertQuiet("EXPUNGE tag survives detached waiter")
	fmt.Fprintf(peer, "* 2 EXPUNGE\r\n%s OK erased\r\n", strings.Fields(erase)[0])
	if _, err = expunge.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := r.ReadString('\n')
	if err != nil || !strings.Contains(next, " UID FETCH 3:3 ") {
		t.Fatal(next, err)
	}
	fmt.Fprintf(peer, "* 2 FETCH (UID 3 BODY[] {1}\r\ny)\r\n%s OK fetched\r\n", strings.Fields(next)[0])
	if err = <-errs; err != nil {
		t.Fatal(err)
	}
	if _, err = (<-commands).Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if s := x.Snapshot(); s.Closed || s.PendingTags != 0 || s.Expunge.Completed != 1 || s.Append.Completed != 1 {
		t.Fatal(s)
	}
}
