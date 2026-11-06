package mailstream

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// Exercise the real streaming IMAP client across the old two-second probe
// boundary: forward UID holes, late arrivals, service samples and cancellation.
func TestControlPollingDoesNotPauseForRedundantNoop(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	var polls, noops atomic.Int32
	started := time.Now()
	payload := append(header(sealed, SessionID{1}, 9), []byte("interactive packet after UID hole")...)
	mail := encodeMail(payload)
	if _, err := decodeMail(mail); err != nil {
		t.Fatal("fixture", err)
	}
	go func() {
		fmt.Fprint(remote, "* PREAUTH [CAPABILITY IMAP4rev1] ready\r\n")
		reader := bufio.NewReader(remote)
		delivered := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return
			}
			tag := fields[0]
			if strings.EqualFold(fields[1], "NOOP") {
				noops.Add(1)
				time.Sleep(100 * time.Millisecond)
			} else if len(fields) > 3 && fields[1] == "UID" && fields[2] == "FETCH" {
				polls.Add(1)
				time.Sleep(8 * time.Millisecond)
				if !delivered && time.Since(started) > 2100*time.Millisecond {
					if fields[3] != "8:4294967295" {
						return
					}
					fmt.Fprintf(remote, "* 1 FETCH (UID 42 RFC822.SIZE %d BODY[] {%d}\r\n%s)\r\n", len(mail), len(mail), mail)
					delivered = true
				} else if delivered && fields[3] != "43:4294967295" {
					return
				}
			} else {
				return
			}
			if _, err = fmt.Fprintf(remote, "%s OK done\r\n", tag); err != nil {
				return
			}
		}
	}()
	client := imapclient.New(local, nil)
	defer client.Close()
	if err := client.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &mailPipe{client: client, after: 7, done: make(chan struct{}), commands: make(chan mailCommand)}
	out := make(chan []byte, 1)
	done := make(chan error, 1)
	go func() { done <- p.receiveControl(ctx, out) }()
	select {
	case got := <-out:
		if !bytes.Equal(got, payload) {
			t.Fatalf("packet %q", got)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("late control packet was not delivered")
	}
	// Let tagged completion advance the forward range and sample an empty poll.
	time.Sleep(30 * time.Millisecond)
	if noops.Load() != 0 {
		t.Errorf("redundant NOOPs paused delivery: %d", noops.Load())
	}
	if polls.Load() < 10 || p.latency.Load() <= 0 {
		t.Fatal("poll service not measured", polls.Load(), p.latency.Load())
	}
	cancel()
	client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked")
	}
}
