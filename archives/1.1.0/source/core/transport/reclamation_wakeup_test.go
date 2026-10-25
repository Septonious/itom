package transport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/pool"
	"itom.local/core/transport"
)

func TestProvenReclamationWakesQuotaBlockedServerWithoutUnrelatedTraffic(t *testing.T) {
	a := authorityPair(t)
	f := a.f
	payload := []byte("one exact slot of durable physical capacity")
	if err := f.server.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	first, err := f.publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	box := &sharedMailbox{keywords: true, reclamation: true, expungeEntered: entered, expungeRelease: release}
	sx, cx := box.selectedWithIdleService(t, "fast", nil, false, false), box.selectedWithIdleService(t, "fast", nil, false, false)
	if _, err = first.Start(context.Background(), sx); err != nil {
		t.Fatal(err)
	}
	dataReceiver, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	si, err := transport.NewServerInbox(sx, transport.DefaultInboxLimits(), a.serverControlReceiver, dataReceiver)
	if err != nil {
		t.Fatal(err)
	}
	if err = si.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	ci, err := transport.NewClientInbox(cx, transport.DefaultInboxLimits(), a.clientControlReceiver, f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err = ci.EnableDeliveryReceipts(); err != nil {
		t.Fatal(err)
	}
	if _, err = ci.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = si.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	held, err := f.store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	limit := f.store.Config().Accounts[0].Users[0].Limit
	reservation, err := f.store.IssueRemote(f.scope, pool.Amount{Bytes: limit.Bytes - held.Bytes, Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	serverReader, err := transport.NewInboxWorker(si, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	clientReader, err := transport.NewInboxWorker(ci, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	writer, err := transport.NewServerPublicationWorker(sx, transport.Fast, f.publisher)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.server.Watch(writer); err != nil {
		t.Fatal(err)
	}
	if err = f.server.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	written := make(chan error, 1)
	go func() { written <- writer.Run(ctx) }()
	waitWorker(t, writer, func(s transport.PublicationStats) bool { return s.DataBlocked })
	stopClient, clientDone := startReclamationReader(t, clientReader)
	stopServer, serverDone := startReclamationReader(t, serverReader)
	released := false
	defer func() {
		stopClient()
		stopServer()
		cancel()
		if !released {
			close(release)
		}
		sx.Close()
		cx.Close()
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual exact-set cleanup did not reach its tag boundary")
	}
	// Avoid another peer receipt for the second body; the first cleanup must be
	// enough to resume this writer, without any later application/control traffic.
	stopClient()
	if err = <-clientDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !writer.Stats().DataBlocked || writer.Stats().Accepted != 0 {
		t.Fatal("expunge/tag state invented quota permission", writer.Stats())
	}
	if now, err := f.store.Held("account1", "12345678"); err != nil || now.Bytes != limit.Bytes {
		t.Fatal("debt released before separate absence proof", now, err)
	}
	close(release)
	released = true
	awaitReclamation(t, func() bool { return si.ReclamationStats().Released == 1 })
	waitWorker(t, writer, func(s transport.PublicationStats) bool { return s.Accepted == 1 && !s.DataBlocked })
	cancel()
	if err = <-written; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	stopServer()
	if err = <-serverDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	still, err := f.store.GetGrant(reservation.Ref)
	if err != nil || still.Closed || still.Released.Bytes != 0 {
		t.Fatal("unrelated original reservation was refunded", still, err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}
