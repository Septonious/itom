package transport_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestCompatibleFlagsPipelineWithoutStarvingExclusiveInventory(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	body := serverBody(t, f, []byte("actual original SERVER body awaiting client MAC"))
	bodyEntered, flagsEntered, inventoryEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	bodyRelease, flagsRelease := make(chan struct{}), make(chan struct{})
	var bodyOnce, flagsOnce, inventoryOnce sync.Once
	x := selectedAccountPeerWithFeatures(t, "account1", "fast", transport.DefaultIMAPLimits(), "UIDPLUS", true, func(c net.Conn) {
		r := bufio.NewReader(c)
		stage := 0
		for {
			if _, err := r.Peek(1); err != nil {
				return
			}
			line := readLine(t, r)
			switch {
			case strings.HasSuffix(line, " (UID FLAGS)\r\n"):
				if stage == 1 {
					stage = 2
					close(flagsEntered)
					<-flagsRelease
				}
				fmt.Fprintf(c, "* 1 FETCH (UID 1 FLAGS ())\r\n%s OK flags without receipt\r\n", tag(line))
			case strings.HasSuffix(line, " (UID)\r\n"):
				if stage == 2 {
					inventoryOnce.Do(func() { close(inventoryEntered) })
				}
				fmt.Fprintf(c, "* 1 FETCH (UID 1)\r\n%s OK inventory\r\n", tag(line))
			case strings.Contains(line, "UID FETCH 1:1 (UID BODY.PEEK[])"):
				mime := body.MIME()
				fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[] {%d}\r\n%s)\r\n%s OK body\r\n", len(mime), mime, tag(line))
			case strings.Contains(line, "UID FETCH 2:2"):
				if stage == 0 {
					stage = 1
					close(bodyEntered)
					<-bodyRelease
				}
				fmt.Fprintf(c, "%s OK empty next UID\r\n", tag(line))
			default:
				t.Error("no client MAC authorized a delete or other command", line)
				return
			}
		}
	})
	remote, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), remote)
	if err != nil {
		t.Fatal(err)
	}
	if err = inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err = inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
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
	go func() { done <- worker.Run(ctx) }()
	defer func() {
		cancel()
		bodyOnce.Do(func() { close(bodyRelease) })
		flagsOnce.Do(func() { close(flagsRelease) })
		<-done
	}()
	wait := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("expected actual parser turn did not progress")
		}
	}
	wait(bodyEntered)
	// Disjoint FLAGS may reach the wire while BODY2 still owns its tag. The
	// scripted provider processes it after the held BODY completion is released.
	awaitReclamation(t, func() bool { return x.Snapshot().Flags.Started > 0 && x.Snapshot().PendingTags == 2 })
	bodyOnce.Do(func() { close(bodyRelease) })
	wait(flagsEntered)
	awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 1 })
	flagsOnce.Do(func() { close(flagsRelease) })
	wait(inventoryEntered) // already waiting discovery goes before another probe
	if now, err := f.store.Held("account1", "12345678"); err != nil || now != held || inbox.ReclamationStats().Released != 0 {
		t.Fatal("fair service fabricated receipt/deletion authority", now, err)
	}
}

func TestCanceledFetchWaiterCannotReleaseAnUndrainedTagOrStrandTheNextTurn(t *testing.T) {
	for _, closeCarrier := range []bool{false, true} {
		t.Run(fmt.Sprintf("close_carrier_%t", closeCarrier), func(t *testing.T) {
			testCanceledFetchTurn(t, closeCarrier)
		})
	}
}

func testCanceledFetchTurn(t *testing.T, closeCarrier bool) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		held := false
		for {
			if _, err := r.Peek(1); err != nil {
				return
			}
			line := readLine(t, r)
			switch {
			case strings.HasSuffix(line, " (UID)\r\n"):
				fmt.Fprintf(c, "* 1 FETCH (UID 1)\r\n%s OK inventory\r\n", tag(line))
			case strings.Contains(line, "UID FETCH 1:1"):
				fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[] {1}\r\nx)\r\n%s OK body\r\n", tag(line))
			default:
				if !held {
					held = true
					close(entered)
					<-release
				}
				fmt.Fprintf(c, "%s OK empty\r\n", tag(line))
			}
		}
	})
	inbox := newTestInbox(t, x, func(_ uint32, _ transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { return nil }), nil
	})
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := inbox.Poll(ctx); done <- err }()
	defer func() { cancel(); once.Do(func() { close(release) }) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("held original command did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	queued, cancelQueued := context.WithCancel(context.Background())
	go func() { _, err := inbox.Poll(queued); done <- err }()
	awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 1 })
	cancelQueued()
	if err := <-done; !errors.Is(err, context.Canceled) || x.Snapshot().FetchWaiters != 0 || !x.Snapshot().FetchActive {
		t.Fatal("cancel released the physical tag or leaked its queued waiter", err, x.Snapshot())
	}
	next, cancelNext := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelNext()
	go func() { _, err := inbox.Poll(next); done <- err }()
	awaitReclamation(t, func() bool { return x.Snapshot().FetchWaiters == 1 })
	if closeCarrier {
		x.Close() // explicit owner shutdown must also unblock a queued turn
		once.Do(func() { close(release) })
		if err := <-done; !errors.Is(err, transport.ErrClosed) {
			t.Fatal("closed carrier stranded a queued turn", err)
		}
		return
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil || x.Snapshot().Closed {
		t.Fatal("next discovery turn was stranded or healthy socket closed", err, x.Snapshot())
	}
}
