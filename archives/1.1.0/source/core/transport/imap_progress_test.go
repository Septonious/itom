package transport_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"itom.local/core/transport"
)

// Real selected TLS sockets, not a fake command clock. A detached application
// cannot extend a missing provider tag or transfer its ambiguity to a retry.
func TestMissingProviderTagsExpireTogetherAfterApplicationDetaches(t *testing.T) {
	limits := transport.DefaultIMAPLimits()
	limits.ProgressTimeout = 300 * time.Millisecond
	entered := make(chan struct{})
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", limits, "", false, func(c net.Conn) {
		r := bufio.NewReader(c)
		for range 2 {
			if line := readLine(t, r); !strings.HasSuffix(line, " NOOP\r\n") {
				t.Error("unexpected command", line)
				return
			}
		}
		close(entered)
		// Remain connected without sending either completion. The executor's
		// original progress lifetime, rather than fixture EOF, must stop it.
		ioBuffer := make([]byte, 1)
		c.Read(ioBuffer)
	})
	ctx, cancel := context.WithCancel(context.Background())
	first, err := x.Noop(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	cancel()
	if _, err := first.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("application waiter did not detach", err)
	}
	if x.Snapshot().Closed || x.Snapshot().PendingTags != 2 {
		t.Fatal("detaching changed the provider command lifetime", x.Snapshot())
	}
	waitCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	for _, cmd := range []*transport.Command{first, second} {
		if _, err := cmd.Wait(waitCtx); !errors.Is(err, transport.ErrUnknown) {
			t.Fatal("missing tag was not retained as unknown", err)
		}
	}
	if s := x.Snapshot(); !s.Closed || s.PendingTags != 0 || s.Noop.Failed != 2 {
		t.Fatal("timeout left live commands or reported useful completion", s)
	}
}

func TestCompletedAndRefusedTagsCannotExpireLaterWorkOnHealthyCarrier(t *testing.T) {
	limits := transport.DefaultIMAPLimits()
	limits.ProgressTimeout = 300 * time.Millisecond
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", limits, "", false, func(c net.Conn) {
		r := bufio.NewReader(c)
		for n := 0; ; n++ {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if n == 0 {
				fmt.Fprintf(c, "%s NO refused\r\n", tag(line))
			} else {
				fmt.Fprintf(c, "%s OK alive\r\n", tag(line))
			}
		}
	})
	for n := range 3 {
		cmd, err := x.Noop(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		_, err = wait(t, cmd)
		if n == 0 && !errors.Is(err, transport.ErrRefused) || n > 0 && err != nil {
			t.Fatal("tag outcome changed", n, err)
		}
		if n == 1 {
			// Both terminal outcomes must stay terminal past their old deadline.
			time.Sleep(2 * limits.ProgressTimeout)
		}
	}
	if s := x.Snapshot(); s.Closed || s.PendingTags != 0 || s.Noop.Completed != 3 || s.Noop.Refused != 1 || s.Noop.Failed != 0 {
		t.Fatal("old timeout invalidated healthy service", s)
	}
}
