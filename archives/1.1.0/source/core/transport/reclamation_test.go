package transport_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/transport"
)

func awaitReclamation(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !predicate() {
		select {
		case <-deadline.C:
			t.Fatal("bounded reclamation condition did not become true")
		case <-tick.C:
		}
	}
}

func startReclamationReader(t *testing.T, w *transport.InboxWorker) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done
}

func TestPeerBodyReclamationRequiresExactAbsenceAndNeverRefundsTheClientWallet(t *testing.T) {
	for _, fault := range []string{"", "store_refused", "expunge_refused", "still_visible", "lost_store_tag", "lost_expunge_tag", "changed_incarnation"} {
		t.Run(fault, func(t *testing.T) {
			f := publications(t, 1<<20, 8)
			p := prepare(t, f, []byte("delivered once before maintenance"))
			box := &sharedMailbox{reclamation: true, reclaimFault: fault}
			x := box.selected(t, "fast")
			if outcome, err := p.Start(context.Background(), x); err != nil || outcome != transport.Accepted {
				t.Fatal(outcome, err)
			}
			i, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
			if err != nil {
				t.Fatal(err)
			}
			if err = i.EnableReclamation(); err != nil {
				t.Fatal(err)
			}
			if progress, err := i.Sweep(context.Background()); err != nil || progress.Applied != 1 {
				t.Fatal("full accounted BODY did not apply", progress, err)
			}
			// Neither successful delivery nor the queued cleanup candidate refunds.
			before, err := f.store.GetGrant(journal.GrantRef{Store: p.Usage().Key.Store, ID: p.Usage().Key.Grant, Scope: journal.Scope{Account: "account1", User: "12345678", Session: f.wallet.Session()}})
			if err != nil || before.Released.Messages != 0 || i.ReclamationStats().Queued != 1 {
				t.Fatal("delivery was substituted for deletion evidence", before, err)
			}
			w, err := transport.NewInboxWorker(i, transport.DefaultInboxServiceLimits())
			if err != nil {
				t.Fatal(err)
			}
			cancel, done := startReclamationReader(t, w)
			if fault == "lost_store_tag" || fault == "lost_expunge_tag" || fault == "changed_incarnation" {
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("ambiguous/incarnation-changing service reported success")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("failed carrier did not stop maintenance")
				}
			} else {
				awaitReclamation(t, func() bool {
					s := i.ReclamationStats()
					return s.Released == 1 || s.Disabled || s.Probes >= 2
				})
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			cancel()
			if fault == "" || fault == "store_refused" || fault == "expunge_refused" || fault == "still_visible" {
				cmd, err := x.Noop(context.Background())
				if err != nil {
					t.Fatal("maintenance lost a healthy account", err)
				}
				if _, err := cmd.Wait(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			after, err := f.store.GetGrant(before.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if fault == "" {
				if after.Released.Messages != 1 || after.Released.Bytes != p.Usage().Attempt.EncodedBytes || i.ReclamationStats().Pending != 0 {
					t.Fatal("exact absence did not release the original physical charge", after, i.ReclamationStats())
				}
			} else if after.Released != before.Released || i.ReclamationStats().Released != 0 {
				t.Fatal("refusal/stale visibility/lost tag/incarnation change released debt", after, i.ReclamationStats())
			}
			got := make([]byte, 128)
			n, err := f.receiver.Read(1, got)
			if err != nil || !bytes.Equal(got[:n], []byte("delivered once before maintenance")) {
				t.Fatal("cleanup changed logical application delivery", n, err)
			}
			entries, err := f.wallet.Entries()
			if err != nil || entries[0].Used != p.Usage().Total || len(entries[0].Pending) != 1 {
				t.Fatal("trusted server cleanup invented a client acknowledgement/allowance", entries, err)
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReclamationCancellationRetainsTheExactExpungeCommandForResume(t *testing.T) {
	f := publications(t, 1<<20, 8)
	p := prepare(t, f, []byte("survives detached maintenance waiter"))
	entered, release := make(chan struct{}), make(chan struct{})
	box := &sharedMailbox{reclamation: true, expungeEntered: entered, expungeRelease: release}
	x := box.selected(t, "fast")
	if _, err := p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	i, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	w, err := transport.NewInboxWorker(i, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, w)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("expunge did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatal(err)
	}
	if x.Snapshot().Expunge.Started != 1 || i.ReclamationStats().Released != 0 {
		close(release)
		t.Fatal("cancellation refunded before proof or duplicated expunge")
	}
	close(release)
	cancel, done = startReclamationReader(t, w)
	awaitReclamation(t, func() bool { return i.ReclamationStats().Released == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s := x.Snapshot(); s.Expunge.Started != 1 || s.Expunge.Completed != 1 || s.Closed {
		t.Fatal("resume repeated deletion or replaced a healthy connection", s)
	}
}

func TestReclamationRequiresIssuerScopeAndActualUIDPlusCapability(t *testing.T) {
	f := publications(t, 1<<20, 8)
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	i, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err = i.EnableReclamation(); !errors.Is(err, transport.ErrUIDPlusRequired) {
		t.Fatal("assumed UIDPLUS from a test/provider identity", err)
	}
	server := serverPublications(t, transport.Fast, 4<<20)
	client, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), server.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnableReclamation(); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("client received issuer deletion/refund authority", err)
	}
}

func TestOwnServerBodyObservationWaitsForAuthenticatedClientReceipt(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	p := serverBody(t, f, []byte("retain until authenticated client delivery evidence"))
	box := &sharedMailbox{reclamation: true}
	x := box.selected(t, "fast")
	if _, err := p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	receiver, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), receiver)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := inbox.ReclamationStats(); stats.Queued != 1 || stats.Pending != 1 || stats.Released != 0 {
		t.Fatal("own observation lost bounded candidate/debt", stats)
	}
	ref, _ := p.ServerAttempt()
	a, err := f.store.GetAttempt(ref)
	if err != nil || a.State != journal.Committed || a.Location.UID != 1 {
		t.Fatal("own BODY proof or original debt lost", a, err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	awaitReclamation(t, func() bool { return inbox.ReclamationStats().ReceiptProbes >= 2 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if stats := x.Snapshot(); stats.Store.Started != 0 || stats.Expunge.Started != 0 || inbox.ReclamationStats().Released != 0 {
		t.Fatal("own echo supplied peer evidence", stats)
	}
	awaitReclamation(t, func() bool { return !x.Snapshot().FetchActive })
	body := fetchAt(t, x, 1)
	delivery, err := f.incoming.Decode(9, body.UID, body.Bytes)
	if err != nil || delivery == nil {
		t.Fatal("own scan interfered with actual peer delivery", err)
	}
	if err := delivery.Apply(); err != nil {
		t.Fatal(err)
	}
}

func TestServerReclamationNeedsTheReceiptForThatExactBodyAndUID(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	one := serverBody(t, f, []byte("first exact body"))
	two := serverBody(t, f, []byte("second exact body"))
	box := &sharedMailbox{reclamation: true, keywords: true}
	sx, cx := box.selected(t, "fast"), box.selected(t, "fast")
	for _, p := range []*transport.PreparedPublication{one, two} {
		if outcome, err := p.Start(context.Background(), sx); err != nil || outcome != transport.Accepted {
			t.Fatal(outcome, err)
		}
	}
	remote, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	si, err := transport.NewServerInbox(sx, transport.DefaultInboxLimits(), remote)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := transport.NewClientInbox(cx, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := si.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if err := ci.EnableDeliveryReceipts(); err != nil {
		t.Fatal(err)
	}
	if _, err := si.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ci.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Held("account1", "12345678")
	if err != nil || before.Messages != 2 || si.ReclamationStats().Released != 0 || ci.DeliveryReceiptStats().Queued != 2 {
		t.Fatal(before, err)
	}
	cw, err := transport.NewInboxWorker(ci, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, cw)
	awaitReclamation(t, func() bool { return ci.DeliveryReceiptStats().Marked == 2 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	readMarker := func(uid uint32) string {
		cmd, err := sx.FetchFlags(context.Background(), uid)
		if err != nil {
			t.Fatal(err)
		}
		bodies, err := cmd.Wait(context.Background())
		if err != nil || len(bodies) != 1 || len(bodies[0].Flags) != 1 {
			t.Fatal("actual delivery metadata", bodies, err)
		}
		return bodies[0].Flags[0]
	}
	marker1, marker2 := readMarker(1), readMarker(2)
	setMarker := func(uid uint32, marker string, add bool) {
		cmd, err := cx.StoreKeyword(context.Background(), uid, marker, add)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cmd.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// A real receipt from another actual body/UID must not authorize deletion.
	setMarker(2, marker2, false)
	setMarker(2, marker1, true)
	sw, err := transport.NewInboxWorker(si, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done = startReclamationReader(t, sw)
	awaitReclamation(t, func() bool { s := si.ReclamationStats(); return s.Released == 1 && s.ReceiptProbes >= 3 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	mid, err := f.store.Held("account1", "12345678")
	if err != nil || mid.Messages != 1 || si.ReclamationStats().Released != 1 {
		t.Fatal("transplanted marker released another charge", mid, si.ReclamationStats(), err)
	}
	if body := fetchAt(t, sx, 2); len(body.Bytes) == 0 {
		t.Fatal("wrong receipt erased the second body")
	}
	setMarker(2, marker2, true)
	cancel, done = startReclamationReader(t, sw)
	awaitReclamation(t, func() bool { return si.ReclamationStats().Released == 2 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, err := f.store.Held("account1", "12345678")
	if err != nil || after.Bytes != 0 || after.Messages != 0 {
		t.Fatal("exact receipt + absence did not release original local debt", after, err)
	}
	// No logical stream Receipt was sent; metadata cannot free replay/send memory.
	if s := f.server.Snapshot(); s.RetainedRecords != 2 || s.SendBuffered == 0 {
		t.Fatal("physical cleanup forged a logical cumulative receipt", s)
	}
	if s := sx.Snapshot(); s.Append.Started != 2 || s.Expunge.Started != 2 || s.Closed {
		t.Fatal("metadata spent more messages/reconnected", s)
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestClientReceiptCannotRaceTheServerAppendCompletionRow(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	p := serverBody(t, f, []byte("body visible before its APPEND tag"))
	entered, gate := make(chan struct{}), make(chan struct{})
	box := &sharedMailbox{reclamation: true, keywords: true, appendEntered: entered, appendRelease: gate}
	publish, reader, client := box.selected(t, "fast"), box.selected(t, "fast"), box.selected(t, "fast")
	doneAppend := make(chan error, 1)
	go func() { _, err := p.Start(context.Background(), publish); doneAppend <- err }()
	released := false
	release := func() {
		if !released {
			close(gate)
			released = true
		}
	}
	t.Cleanup(release)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("APPEND did not become visible")
	}
	remote, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	si, err := transport.NewServerInbox(reader, transport.DefaultInboxLimits(), remote)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := transport.NewClientInbox(client, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := si.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if err := ci.EnableDeliveryReceipts(); err != nil {
		t.Fatal(err)
	}
	if _, err := si.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := ci.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	cw, err := transport.NewInboxWorker(ci, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	sw, err := transport.NewInboxWorker(si, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancelClient, doneClient := startReclamationReader(t, cw)
	awaitReclamation(t, func() bool { return ci.DeliveryReceiptStats().Marked == 1 })
	cancelServer, doneServer := startReclamationReader(t, sw)
	awaitReclamation(t, func() bool { return sw.Stats().Turns >= 2 })
	if stats := reader.Snapshot(); stats.Expunge.Started != 0 || si.ReclamationStats().Released != 0 {
		t.Fatal("peer delivery bypassed active APPEND exclusion", stats)
	}
	release()
	if err := <-doneAppend; err != nil {
		t.Fatal("cleanup raced original journal completion", err)
	}
	awaitReclamation(t, func() bool { return si.ReclamationStats().Released == 1 })
	cancelClient()
	cancelServer()
	for _, done := range []<-chan error{doneClient, doneServer} {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}
