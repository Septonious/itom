package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func waitWorker(t *testing.T, w *transport.PublicationWorker, ready func(transport.PublicationStats) bool) {
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
			t.Fatal("publication worker made no expected progress", w.Stats())
		}
	}
}

func TestPublicationWorkerDoesNotBlockFetchBehindAnAppendCompletion(t *testing.T) {
	f := publications(t, 50000, 10)
	entered := make(chan struct{})
	var own []byte
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		line := readLine(t, r)
		_, own = readAppendLiteral(t, c, r, line)
		if own == nil {
			return
		}
		close(entered)
		fetch := readLine(t, r)
		fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[] {%d}\r\n%s)\r\n%s OK fetched\r\n%s OK stored\r\n", len(own), own, tag(fetch), tag(line))
		noop := readLine(t, r)
		fmt.Fprintf(c, "%s OK still selected\r\n", tag(noop))
	})
	w, err := transport.NewClientPublicationWorker(x, transport.Fast, f.publisher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewClientPublicationWorker(x, transport.Fast, f.publisher); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("two publisher owners claimed one executor", err)
	}
	if err = f.sender.Queue(1, []byte("application bytes"), time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer cancel()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("APPEND never started")
	}
	if err = w.Run(context.Background()); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("concurrent worker Run accepted", err)
	}
	command, err := x.Fetch(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	bodies, err := command.Wait(context.Background())
	if err != nil || len(bodies) != 1 {
		t.Fatal("independent FETCH waited on APPEND tag", err)
	}
	if !bytes.Equal(bodies[0].Bytes, own) {
		t.Fatal("overlapped FETCH changed stored bytes")
	}
	waitWorker(t, w, func(s transport.PublicationStats) bool { return s.Accepted == 1 })
	if stats := w.Stats(); stats.AcceptedEncodedBytes != uint64(len(bodies[0].Bytes)) || stats.Prepared != 1 {
		t.Fatal(stats)
	}
	if f.sender.Snapshot().SendBuffered == 0 {
		t.Fatal("provider acceptance invented peer delivery")
	}
	cancel()
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	command, err = x.Noop(context.Background())
	if err != nil {
		t.Fatal("worker cancellation closed carrier", err)
	}
	if _, err = command.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func clientWorkerFixture(t *testing.T) (*serverFixture, *wallet.Wallet, *transport.ClientPublisher) {
	t.Helper()
	f := serverPublications(t, transport.Fast, 4<<20)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), f.store.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	g, err := f.store.IssueRemote(f.scope, pool.Amount{Bytes: 50000, Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := f.store.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	p, err := transport.NewClientPublisher(f.client, transport.Fast, transport.DataLedger, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	return f, w, p
}

func TestExhaustedPublicationWaitsForRealPermissionWithoutRespending(t *testing.T) {
	f, w, p := clientWorkerFixture(t)
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	defer x.Close()
	worker, err := transport.NewClientPublicationWorker(x, transport.Fast, p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	if err = f.client.Queue(1, []byte("first"), time.Now()); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker, func(s transport.PublicationStats) bool { return s.Accepted == 1 })
	if err = f.client.Queue(1, []byte("second"), time.Now()); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker, func(s transport.PublicationStats) bool { return s.DataBlocked })
	if worker.Stats().Prepared != 1 || worker.Pending() != nil {
		t.Fatal("permission wait created a speculative body", worker.Stats())
	}
	entries, err := w.Entries()
	if err != nil || len(entries) != 1 || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal(entries, err)
	}
	g, err := f.store.IssueRemote(f.scope, pool.Amount{Bytes: 50000, Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := f.store.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	worker.NotifyPermission()
	waitWorker(t, worker, func(s transport.PublicationStats) bool { return s.Accepted == 2 })
	entries, err = w.Entries()
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	for _, e := range entries {
		if e.Used.Messages != 1 || len(e.Pending) != 1 || e.Fenced {
			t.Fatal("new permission rewrote old exposure", entries)
		}
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicationUncertaintyRetainsOneExactBodyAndNeverAutomaticallyReappends(t *testing.T) {
	f := publications(t, 50000, 10)
	var appends atomic.Int32
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		line := readLine(t, r)
		_, body := readAppendLiteral(t, c, r, line)
		if body != nil {
			appends.Add(1)
		} /* lost tagged reply */
	})
	w, err := transport.NewClientPublicationWorker(x, transport.Fast, f.publisher)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.sender.Queue(1, []byte("uncertain application bytes"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = w.Run(context.Background()); err == nil {
		t.Fatal("lost reply reported success")
	}
	pending := w.Pending()
	if pending == nil || len(pending.MIME()) == 0 {
		t.Fatal("uncertain exact body lost")
	}
	if err = w.Run(context.Background()); err == nil {
		t.Fatal("uncertainty automatically resumed writes")
	}
	if appends.Load() != 1 || w.Stats().Prepared != 1 || w.Stats().Uncertain != 1 || w.Pending() != pending {
		t.Fatal("uncertain publication was retried", w.Stats(), appends.Load())
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal(entries, err)
	}
	if f.sender.Snapshot().SendBuffered == 0 {
		t.Fatal("lost reply discarded unreceived logical bytes")
	}
}

func TestFastPublicationLedgersHaveIndependentPermissionAndFairTurns(t *testing.T) {
	f, w, data := clientWorkerFixture(t)
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 1<<20, 32)
	cw, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), control.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()
	g, err := control.IssueRemote(f.scope, pool.Amount{Bytes: 50000, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := control.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = cw.Accept(offer); err != nil {
		t.Fatal(err)
	}
	protocol, err := transport.NewClientPublisher(f.client, transport.Fast, transport.ControlLedger, f.clientKey, cw, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.client.Receive(incoming(transport.Data, 1, 0, []byte("peer"))); err != nil {
		t.Fatal(err)
	}
	if err = f.client.Queue(1, []byte("short response"), time.Now()); err != nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	defer x.Close()
	worker, err := transport.NewClientPublicationWorker(x, transport.Fast, data, protocol)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitWorker(t, worker, func(s transport.PublicationStats) bool { return s.Accepted == 2 })
	box.mu.Lock()
	bodies := append([][]byte(nil), box.bodies...)
	box.mu.Unlock()
	if len(bodies) != 2 {
		t.Fatal("unexpected extra physical work", len(bodies))
	}
	for index, expected := range []wire.ID{control.StoreID(), f.store.StoreID()} {
		body, err := mailbox.Decode(bodies[index])
		if err != nil {
			t.Fatal(err)
		}
		charge, err := mailbox.CanonicalEncodedBytes(bodies[index])
		if err != nil {
			t.Fatal(err)
		}
		usage, _, err := wire.OpenDataEnvelope(body, f.key, charge)
		if err != nil || usage.Key.Store != expected {
			t.Fatal("ledger turn used wrong authority", index, err)
		}
	}
	entries, err := w.Entries()
	if err != nil || entries[0].Used.Messages != 1 {
		t.Fatal(entries, err)
	}
	entries, err = cw.Entries()
	if err != nil || entries[0].Used.Messages != 1 {
		t.Fatal(entries, err)
	}
	if err = f.client.Queue(1, []byte("DATA waits for another grant"), time.Now()); err != nil {
		t.Fatal(err)
	}
	waitWorker(t, worker, func(s transport.PublicationStats) bool { return s.DataBlocked })
	buf := make([]byte, 4)
	if n, err := f.client.Read(1, buf); err != nil || n != 4 {
		t.Fatal(n, err)
	} // creates receiver credit, not DATA permission
	waitWorker(t, worker, func(s transport.PublicationStats) bool { return s.Accepted == 3 })
	if !worker.Stats().DataBlocked {
		t.Fatal("protocol service invented DATA allowance")
	}
	entries, err = w.Entries()
	if err != nil || entries[0].Used.Messages != 1 {
		t.Fatal("protocol borrowed exhausted DATA grant", entries, err)
	}
}

func TestPublicationWorkerRejectsWrongAccountOrMailboxBeforeIO(t *testing.T) {
	f := publications(t, 50000, 10)
	for _, route := range []struct{ account, folder string }{{"wrong", "fast"}, {"account1", "bulk"}} {
		x := selectedAccountPeer(t, route.account, route.folder, func(c net.Conn) {
			if _, err := bufio.NewReader(c).Peek(1); err == nil {
				t.Error("wrong worker scope initiated I/O")
			}
		})
		if _, err := transport.NewClientPublicationWorker(x, transport.Fast, f.publisher); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal(err)
		}
		x.Close()
	}
}
