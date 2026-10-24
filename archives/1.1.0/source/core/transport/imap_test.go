package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"itom.local/core/transport"
	"net"
	"strings"
	"testing"
	"time"
)

// These protocol scenarios use actual OS TCP sockets. The scripted peer is an
// ordering/failure oracle, not a performance model or a Rambler prediction.
func executor(t *testing.T, script func(net.Conn)) (*transport.Executor, <-chan struct{}) {
	t.Helper()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	accepted := make(chan struct{})
	go func() {
		defer close(done)
		c, e := listener.Accept()
		close(accepted)
		if e != nil {
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		script(c)
	}()
	c, e := net.Dial("tcp", listener.Addr().String())
	<-accepted
	listener.Close()
	if e != nil {
		t.Fatal(e)
	}
	l := transport.DefaultIMAPLimits()
	l.ProgressTimeout = 2 * time.Second
	x, e := transport.NewSelectedExecutor(c, bufio.NewReader(c), l)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		x.Close()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("peer did not stop")
		}
	})
	return x, done
}
func tag(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "missing"
	}
	return f[0]
}
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	s, e := r.ReadString('\n')
	if e != nil {
		t.Error(e)
	}
	return s
}
func wait(t *testing.T, c *transport.Command) ([]transport.Body, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.Wait(ctx)
}
func TestAppendAndFetchCompleteIndependentlyOnOneSocket(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		appendTag := tag(readLine(t, r))
		fmt.Fprint(c, "+ literal permitted\r\n")
		b := make([]byte, 7)
		if _, e := io.ReadFull(r, b); e != nil {
			t.Error(e)
		}
		if string(b) != "hello\r\n" {
			t.Errorf("literal %q", b)
		}
		fetchTag := tag(readLine(t, r))
		fmt.Fprintf(c, "* 1 FETCH (UID 9 BODY[] {5}\r\nworld)\r\n%s OK fetched\r\n%s OK appended\r\n", fetchTag, appendTag)
		noopTag := tag(readLine(t, r))
		fmt.Fprintf(c, "%s NO refused\r\n", noopTag)
		noopTag = tag(readLine(t, r))
		fmt.Fprintf(c, "%s OK alive\r\n", noopTag)
	})
	appendCmd, e := x.Append(context.Background(), "fast", []byte("hello"))
	if e != nil {
		t.Fatal(e)
	}
	fetchCmd, e := x.Fetch(context.Background(), 9, 9)
	if e != nil {
		t.Fatal(e)
	}
	bodies, e := wait(t, fetchCmd)
	if e != nil || len(bodies) != 1 || string(bodies[0].Bytes) != "world" {
		t.Fatal(bodies, e)
	}
	if _, e = wait(t, appendCmd); e != nil {
		t.Fatal(e)
	}
	c, e := x.Noop(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, c); !errors.Is(e, transport.ErrRefused) {
		t.Fatal("tagged NO lost", e)
	}
	c, e = x.Noop(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, c); e != nil {
		t.Fatal("tagged refusal killed unrelated service", e)
	}
}
func TestCanceledApplicationDoesNotBreakStartedLiteralOrSharedConnection(t *testing.T) {
	seen := make(chan struct{})
	permit := make(chan struct{})
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		a := tag(readLine(t, r))
		close(seen)
		<-permit
		fmt.Fprint(c, "+ continue\r\n")
		b := make([]byte, 6)
		if _, e := io.ReadFull(r, b); e != nil {
			t.Error(e)
		}
		if !bytes.Equal(b, []byte("body\r\n")) {
			t.Error("literal not drained")
		}
		fmt.Fprintf(c, "%s OK\r\n", a)
		a = tag(readLine(t, r))
		fmt.Fprintf(c, "%s OK\r\n", a)
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan *transport.Command, 1)
	errs := make(chan error, 1)
	go func() { c, e := x.Append(ctx, "bulk", []byte("body")); result <- c; errs <- e }()
	<-seen
	cancel()
	close(permit)
	cmd := <-result
	if e := <-errs; e != nil {
		t.Fatal("started literal interrupted", e)
	}
	if _, e := cmd.Wait(ctx); e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := wait(t, cmd); e != nil {
		t.Fatal(e)
	}
	cmd, e := x.Noop(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, cmd); e != nil {
		t.Fatal(e)
	}
}
func TestRefusalBeforeLiteralDoesNotDeadlockTheWriter(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		a := tag(readLine(t, r))
		fmt.Fprintf(c, "%s NO quota\r\n", a)
		a = tag(readLine(t, r))
		fmt.Fprintf(c, "%s OK\r\n", a)
	})
	cmd, e := x.Append(context.Background(), "fast", []byte("charged"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, cmd); !errors.Is(e, transport.ErrRefused) {
		t.Fatal(e)
	}
	cmd, e = x.Noop(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, cmd); e != nil {
		t.Fatal(e)
	}
}
func TestUnknownAppendIsNotReclassifiedAsRetryableRefusal(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		readLine(t, r)
		fmt.Fprint(c, "+\r\n")
		io.CopyN(io.Discard, r, 6)
	})
	cmd, e := x.Append(context.Background(), "bulk", []byte("body"))
	if e != nil && !errors.Is(e, transport.ErrUnknown) {
		t.Fatal(e)
	}
	if _, e = wait(t, cmd); !errors.Is(e, transport.ErrUnknown) {
		t.Fatal("ambiguous publication became retryable", e)
	}
}
func TestOversizedLiteralFailsBeforeReadingItsPayload(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		readLine(t, r)
		fmt.Fprint(c, "* 1 FETCH (UID 9 BODY[] {2147483647}\r\n")
		io.Copy(io.Discard, r)
	})
	cmd, e := x.Fetch(context.Background(), 9, 9)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, cmd); !errors.Is(e, transport.ErrCapacity) {
		t.Fatal(e)
	}
}
func TestUnrequestedUIDCannotBeDeliveredByAConcurrentFetch(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		a := tag(readLine(t, r))
		fmt.Fprintf(c, "* 1 FETCH (UID 99 BODY[] {1}\r\nx)\r\n%s OK\r\n", a)
	})
	cmd, e := x.Fetch(context.Background(), 9, 9)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = wait(t, cmd); !errors.Is(e, transport.ErrProtocol) {
		t.Fatal(e)
	}
}

func TestFlagsOnlyFetchDoesNotKillOrCompleteTheBodyCommand(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		a := tag(readLine(t, r))
		fmt.Fprintf(c, "* 3 FETCH (UID 8 FLAGS (\\Seen))\r\n* 1 FETCH (UID 9 BODY[] {1}\r\nx)\r\n%s OK\r\n", a)
	})
	cmd, e := x.Fetch(context.Background(), 9, 9)
	if e != nil {
		t.Fatal(e)
	}
	bodies, e := wait(t, cmd)
	if e != nil || len(bodies) != 1 || bodies[0].UID != 9 {
		t.Fatal("flags update disturbed requested body", bodies, e)
	}
}

func TestCanceledQueuedCommandDoesNotWaitForAnotherLiteralBarrier(t *testing.T) {
	seen := make(chan struct{})
	permit := make(chan struct{})
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		a := tag(readLine(t, r))
		close(seen)
		<-permit
		fmt.Fprint(c, "+\r\n")
		io.CopyN(io.Discard, r, 6)
		fmt.Fprintf(c, "%s OK\r\n", a)
	})
	appended := make(chan *transport.Command, 1)
	errs := make(chan error, 1)
	go func() { c, e := x.Append(context.Background(), "bulk", []byte("body")); appended <- c; errs <- e }()
	<-seen
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	queued := make(chan error, 1)
	go func() { _, e := x.Fetch(ctx, 1, 1); queued <- e }()
	select {
	case e := <-queued:
		if !errors.Is(e, context.Canceled) {
			t.Error(e)
		}
	case <-time.After(time.Second):
		t.Error("unstarted app cancel waited on provider barrier")
	}
	close(permit)
	cmd := <-appended
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	if _, e := wait(t, cmd); e != nil {
		t.Fatal(e)
	}
}
