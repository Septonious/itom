package transport_test

import (
	"bufio"
	"bytes"
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

func waitInboxWorker(t *testing.T, w *transport.InboxWorker, ready func(transport.InboxStats) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		changed := w.Changes()
		if ready(w.Stats()) {
			return
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("inbox worker made no expected progress", w.Stats())
		}
	}
}

func TestInboxWorkerFindsMailWithoutNotificationsAndRestartKeepsOriginalDebt(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	box := &sharedMailbox{}
	x, writer := box.selected(t, "fast"), box.selected(t, "fast")
	defer x.Close()
	defer writer.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	w, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("two receive workers claimed one selected socket", err)
	}
	conn, err := f.client.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for run := range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		defer cancel()
		waitInboxWorker(t, w, func(s transport.InboxStats) bool { return s.Turns > uint64(run) })
		payload := bytes.Repeat([]byte{byte(run + 1)}, 419)
		p := serverBody(t, f, payload)
		if outcome, err := p.Start(context.Background(), writer); err != nil || outcome != transport.Accepted {
			t.Fatal("physical publication", outcome, err)
		}
		held, err := f.store.Held(f.scope.Account, f.scope.User)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetReadDeadline(time.Now().Add(time.Second))
		got := make([]byte, len(payload))
		if _, err = io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
			t.Fatal("unannounced real UID mail stranded or changed", err)
		}
		cancel()
		if err = <-done; !errors.Is(err, context.Canceled) {
			t.Fatal("receiver cancellation", err)
		}
		if after, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || held != after {
			t.Fatal("discovery/worker restart released physical debt", held, after, err)
		}
	}
	cmd, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal("receive cancellation replaced the carrier", err)
	}
	if _, err = cmd.Wait(context.Background()); err != nil {
		t.Fatal("selected carrier lost after receive restart", err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestBulkInboxIdleUsesRealBodyProofAndAuditsMissingNotifications(t *testing.T) {
	for _, silent := range []bool{true, false} {
		t.Run(fmt.Sprintf("missing_notifications_%t", silent), func(t *testing.T) {
			f := serverPublications(t, transport.Bulk, 4<<20)
			box := &sharedMailbox{}
			x := box.selectedWithIdleService(t, "bulk", nil, true, silent)
			writer := box.selected(t, "bulk")
			defer x.Close()
			defer writer.Close()
			inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming)
			if err != nil {
				t.Fatal(err)
			}
			limits := transport.DefaultInboxServiceLimits()
			limits.PreferIdle, limits.IdleAudit = true, 25*time.Millisecond
			w, err := transport.NewInboxWorker(inbox, limits)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- w.Run(ctx) }()
			defer func() { cancel(); <-done }()
			waitInboxWorker(t, w, func(s transport.InboxStats) bool { return s.IdleAttempts > 0 })
			payload := bytes.Repeat([]byte{23, 0, 0xff}, 32<<10)
			p := serverBody(t, f, payload)
			if outcome, err := p.Start(context.Background(), writer); err != nil || outcome != transport.Accepted {
				t.Fatal(outcome, err)
			}
			held, err := f.store.Held(f.scope.Account, f.scope.User)
			if err != nil {
				t.Fatal(err)
			}
			conn, err := f.client.Conn(1)
			if err != nil {
				t.Fatal(err)
			}
			conn.SetReadDeadline(time.Now().Add(time.Second))
			got := make([]byte, len(payload))
			if _, err = io.ReadFull(conn, got); err != nil || !bytes.Equal(got, payload) {
				t.Fatal("IDLE/audit did not deliver exact authenticated application bytes", err)
			}
			if after, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || after != held {
				t.Fatal("notification refunded physical debt", held, after, err)
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCanceledInboxFetchDrainsBeforeRestartWithoutReplacingTheCarrier(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		first := readLine(t, r)
		close(entered)
		<-release
		fmt.Fprintf(c, "%s OK delayed inventory\r\n", tag(first))
		for {
			if _, err := r.Peek(1); err != nil {
				return
			}
			line := readLine(t, r)
			if !strings.Contains(line, "UID FETCH") && !strings.Contains(line, "NOOP") {
				t.Error("unexpected receive restart command", line)
				return
			}
			fmt.Fprintf(c, "%s OK healthy\r\n", tag(line))
		}
	})
	defer x.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	w, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { first <- w.Run(ctx) }()
	defer cancel()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("inventory did not start")
	}
	cancel()
	if err = <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	second := make(chan error, 1)
	go func() { second <- w.Run(ctx2) }()
	waitInboxWorker(t, w, func(s transport.InboxStats) bool { return s.CapacityWaits > 0 })
	if err = w.Run(context.Background()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("concurrent reader accepted", err)
	}
	once.Do(func() { close(release) })
	waitInboxWorker(t, w, func(s transport.InboxStats) bool { return s.Turns > 0 })
	cancel2()
	if err = <-second; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cmd, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal("old canceled FETCH poisoned the selected socket", err)
	}
	if _, err = cmd.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedExistsAndStatusCannotCreateASelfPollingNotificationLoop(t *testing.T) {
	x, _ := executor(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		for turn := range 3 {
			line := readLine(t, r)
			count := 4
			if turn == 2 {
				count = 5
			}
			fmt.Fprintf(c, "* OK [ALERT] informational\r\n* %d EXISTS\r\n%s OK healthy\r\n", count, tag(line))
		}
	})
	for turn := range 3 {
		cmd, err := x.Noop(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cmd.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-x.Changes():
			if turn == 1 {
				t.Fatal("unchanged EXISTS or status created a self-poll edge")
			}
		default:
			if turn != 1 {
				t.Fatal("real mailbox count change lost its hint")
			}
		}
	}
}

func TestFullInboxInventoryStopsForRecoveryWithoutSpinReconnectOrRefund(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	first := serverBody(t, f, []byte("first unretired body"))
	second := serverBody(t, f, []byte("second unretired body"))
	box := &sharedMailbox{bodies: [][]byte{first.MIME(), second.MIME()}, bytes: len(first.MIME()) + len(second.MIME())}
	x := box.selected(t, "fast")
	defer x.Close()
	limits := transport.DefaultInboxLimits()
	limits.KnownUIDs = 1
	inbox, err := transport.NewClientInbox(x, limits, f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	w, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	held, err := f.store.Held(f.scope.Account, f.scope.User)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = w.Run(ctx); !errors.Is(err, transport.ErrCapacity) {
		t.Fatal("full inventory was retried as transient command capacity", err)
	}
	cmd, err := x.Noop(context.Background())
	if err != nil {
		t.Fatal("inventory bound discarded healthy carrier", err)
	}
	if _, err = cmd.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after, err := f.store.Held(f.scope.Account, f.scope.User); err != nil || after != held {
		t.Fatal("inventory capacity was treated as physical reclamation", held, after, err)
	}
}
