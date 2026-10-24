package transport_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"itom.local/core/transport"
)

func idleExecutor(t *testing.T, script func(net.Conn)) *transport.Executor {
	t.Helper()
	x := selectedAccountPeerWithCapabilities(t, "account1", "bulk", transport.DefaultIMAPLimits(), "IDLE", script)
	if !x.IdleSupported() {
		t.Fatal("verified capability did not reach the executor")
	}
	return x
}

func TestIdleWriterPreemptionDrainsDoneAndTagBeforeTheNextCommand(t *testing.T) {
	for _, beforeContinuation := range []bool{true, false} {
		t.Run(fmt.Sprintf("request_before_continuation_%t", beforeContinuation), func(t *testing.T) {
			entered, ready, release, doneSeen, releaseTag := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			wireBytes := make(chan error, 1)
			var releaseOnce, tagOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			defer tagOnce.Do(func() { close(releaseTag) })
			x := idleExecutor(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				idle := readLine(t, r)
				if !strings.Contains(idle, " IDLE") {
					t.Error("IDLE missing", idle)
					return
				}
				close(entered)
				<-release
				fmt.Fprint(c, "+ idling\r\n")
				close(ready)
				if line := readLine(t, r); line != "DONE\r\n" {
					t.Error("command crossed IDLE continuation", line)
					return
				}
				go func() { _, err := r.Peek(1); wireBytes <- err }()
				close(doneSeen)
				<-releaseTag
				fmt.Fprintf(c, "%s OK idle complete\r\n", tag(idle))
				if err := <-wireBytes; err != nil {
					return
				}
				noop := readLine(t, r)
				if !strings.Contains(noop, " NOOP") {
					t.Error("preempting command missing", noop)
					return
				}
				fmt.Fprintf(c, "%s OK healthy\r\n", tag(noop))
			})
			defer x.Close()
			idleDone := make(chan error, 1)
			go func() { idleDone <- x.WaitIdle(context.Background(), nil, 2*time.Second) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("IDLE not issued")
			}
			if !beforeContinuation {
				releaseOnce.Do(func() { close(release) })
				<-ready
			}
			commandDone := make(chan error, 1)
			go func() {
				command, err := x.Noop(context.Background())
				if err == nil {
					_, err = command.Wait(context.Background())
				}
				commandDone <- err
			}()
			releaseOnce.Do(func() { close(release) })
			select {
			case <-doneSeen:
			case <-time.After(time.Second):
				t.Fatal("queued writer did not request DONE")
			}
			select {
			case err := <-commandDone:
				t.Fatal("command completed ahead of IDLE tag", err)
			default:
			}
			select {
			case err := <-wireBytes:
				t.Fatal("command bytes crossed the IDLE completion barrier", err)
			case <-time.After(20 * time.Millisecond):
			}
			tagOnce.Do(func() { close(releaseTag) })
			if err := <-idleDone; err != nil {
				t.Fatal(err)
			}
			if err := <-commandDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCanceledIdleWaitDrainsFramingWithoutClosingASelectedCarrier(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	x := idleExecutor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		idle := readLine(t, r)
		close(entered)
		<-release
		fmt.Fprint(c, "+ ready\r\n")
		if line := readLine(t, r); line != "DONE\r\n" {
			t.Error("cancel skipped DONE", line)
			return
		}
		fmt.Fprintf(c, "%s OK complete\r\n", tag(idle))
		noop := readLine(t, r)
		fmt.Fprintf(c, "%s OK selected\r\n", tag(noop))
	})
	defer x.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- x.WaitIdle(ctx, nil, time.Second) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("IDLE not started")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatal("cancellation abandoned the pending continuation", err)
	default:
	}
	once.Do(func() { close(release) })
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	command, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal("cancel killed shared socket", err)
	}
}

func TestIdleRefusalDisablesOnlyNotificationModeAndLeavesUIDDiscoveryUsable(t *testing.T) {
	x := idleExecutor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		idle := readLine(t, r)
		fmt.Fprintf(c, "%s NO backend does not implement IDLE\r\n", tag(idle))
		fetch := readLine(t, r)
		if !strings.Contains(fetch, " UID FETCH ") {
			t.Error("refusal sent DONE or retried IDLE", fetch)
			return
		}
		fmt.Fprintf(c, "%s OK no messages\r\n", tag(fetch))
	})
	defer x.Close()
	if err := x.WaitIdle(context.Background(), nil, time.Second); !errors.Is(err, transport.ErrRefused) || x.IdleSupported() {
		t.Fatal("IDLE rejection did not downgrade this executor", err)
	}
	if err := x.WaitIdle(context.Background(), nil, time.Second); !errors.Is(err, transport.ErrRefused) {
		t.Fatal(err)
	}
	command, err := x.Fetch(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bodies, err := command.Wait(context.Background()); err != nil || len(bodies) != 0 {
		t.Fatal(bodies, err)
	}
}

func TestIdleNotificationIsAHintAndSilentNotificationStillGetsBoundedAudit(t *testing.T) {
	for _, notify := range []bool{true, false} {
		t.Run(fmt.Sprintf("notification_%t", notify), func(t *testing.T) {
			x := idleExecutor(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				idle := readLine(t, r)
				fmt.Fprint(c, "+ ready\r\n")
				if notify {
					fmt.Fprint(c, "* 1 EXISTS\r\n")
				}
				if line := readLine(t, r); line != "DONE\r\n" {
					t.Error("IDLE wake framing", line)
					return
				}
				fmt.Fprintf(c, "%s OK complete\r\n", tag(idle))
				fetch := readLine(t, r)
				fmt.Fprintf(c, "* 1 FETCH (UID 7 BODY[] {4}\r\nreal)\r\n%s OK fetched\r\n", tag(fetch))
			})
			defer x.Close()
			if err := x.WaitIdle(context.Background(), nil, 15*time.Millisecond); err != nil {
				t.Fatal(err)
			}
			command, err := x.Fetch(context.Background(), 7, 7)
			if err != nil {
				t.Fatal(err)
			}
			bodies, err := command.Wait(context.Background())
			if err != nil || len(bodies) != 1 || string(bodies[0].Bytes) != "real" {
				t.Fatal("EXISTS/audit replaced actual BODY", bodies, err)
			}
		})
	}
}

func TestNoIdleCapabilitySendsNoCommandAndKeepsTheOrdinaryExecutor(t *testing.T) {
	x := selectedPeerOn(t, "bulk", func(c net.Conn) {
		r := bufio.NewReader(c)
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			t.Error(err)
			return
		}
		if !strings.Contains(line, " NOOP") {
			t.Error("unnegotiated IDLE reached the wire", line)
			return
		}
		fmt.Fprintf(c, "%s OK healthy\r\n", tag(line))
	})
	defer x.Close()
	if x.IdleSupported() {
		t.Fatal("unadvertised IDLE enabled")
	}
	if err := x.WaitIdle(context.Background(), nil, time.Second); !errors.Is(err, transport.ErrRefused) {
		t.Fatal(err)
	}
	command, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIdleDoneRefusalDowngradesWithoutPoisoningTheSelectedConnection(t *testing.T) {
	x := idleExecutor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		idle := readLine(t, r)
		fmt.Fprint(c, "+ ready\r\n")
		if line := readLine(t, r); line != "DONE\r\n" {
			t.Error("missing DONE", line)
			return
		}
		fmt.Fprintf(c, "%s NO completed with refusal\r\n", tag(idle))
		noop := readLine(t, r)
		fmt.Fprintf(c, "%s OK still selected\r\n", tag(noop))
	})
	defer x.Close()
	wake := make(chan struct{})
	close(wake)
	if err := x.WaitIdle(context.Background(), wake, time.Second); !errors.Is(err, transport.ErrRefused) || x.IdleSupported() {
		t.Fatal("DONE rejection did not downgrade this connection", err)
	}
	command, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestIdleResponseFloodKeepsTheOriginalPerCommandByteBound(t *testing.T) {
	limits := transport.DefaultIMAPLimits()
	limits.LiteralBytes, limits.ResponseBytes = 128, 512
	x := selectedAccountPeerWithCapabilities(t, "account1", "bulk", limits, "IDLE", func(c net.Conn) {
		r := bufio.NewReader(c)
		readLine(t, r)
		fmt.Fprint(c, "+ ready\r\n")
		fmt.Fprint(c, "* OK "+strings.Repeat("x", 300)+"\r\n")
		fmt.Fprint(c, "* OK "+strings.Repeat("y", 300)+"\r\n")
		io.Copy(io.Discard, c)
	})
	defer x.Close()
	if err := x.WaitIdle(context.Background(), nil, time.Second); !errors.Is(err, transport.ErrCapacity) {
		t.Fatal("IDLE bypassed the response-byte ceiling", err)
	}
}

func TestIdleCannotCrossAnExistingPendingTag(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	x := idleExecutor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		noop := readLine(t, r)
		close(entered)
		<-release
		fmt.Fprintf(c, "%s OK pending command complete\r\n", tag(noop))
		next := readLine(t, r)
		if !strings.Contains(next, " NOOP") {
			t.Error("IDLE crossed a pending tag", next)
			return
		}
		fmt.Fprintf(c, "%s OK healthy\r\n", tag(next))
	})
	defer x.Close()
	command, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("pending command missing")
	}
	if err := x.WaitIdle(context.Background(), nil, time.Second); !errors.Is(err, transport.ErrCapacity) {
		t.Fatal("IDLE entered with a pending tag", err)
	}
	once.Do(func() { close(release) })
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal("IDLE attempt abandoned existing tag", err)
	}
	command, err = x.Noop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}
