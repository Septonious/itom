package transport_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestDataAdmissionDefersBeforeReplayAndDoesNotRefetchBlockedUID(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	if err := f.client.Queue(1, []byte("request"), time.Now()); err != nil {
		t.Fatal(err)
	}
	data := take(t, f.client, transport.Fast, transport.MaxPayload)
	complete(t, data, transport.Accepted)
	if err := f.server.Receive(data.Record); err != nil {
		t.Fatal(err)
	}
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 1<<20, 32)
	publisher, err := transport.NewServerPublisher(f.server, transport.Fast, transport.ControlLedger, f.serverKey, control, f.scope, "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || receipt == nil {
		t.Fatal(err)
	}
	response := serverBody(t, f, []byte("response after space returns"))
	origin := f.origin
	origin.Store = control.StoreID()
	origin.Run = control.Run()
	r, err := transport.NewClientReceiver(f.client, transport.ControlLedger, f.clientKey, origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	m := &inboxInventory{uids: []uint32{1, 2}, bodies: map[uint32]string{1: string(response.MIME()), 2: string(receipt.MIME())}, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming, r)
	if err != nil {
		t.Fatal(err)
	}
	release := transport.SeedInboxBlockedForTest(inbox, 7, 8)
	defer release()
	for range 3 {
		if _, err = inbox.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if state := f.client.Snapshot(); state.SendBuffered != 0 || state.ReceiveBuffered != 0 {
		t.Fatal("DATA gate suppressed receipt or consumed payload while full", state)
	}
	m.mu.Lock()
	fetched := m.fetches[1]
	m.mu.Unlock()
	if fetched != 1 {
		t.Fatal("blocked immutable UID repeatedly fetched", fetched)
	}
	release()
	if p, err := inbox.Sweep(context.Background()); err != nil || p.Applied != 3 {
		t.Fatal("deferred DATA could not authenticate after capacity returned", p, err)
	}
	got := make([]byte, 64)
	n, err := f.client.Read(1, got)
	if err != nil || !bytes.Equal(got[:n], []byte("response after space returns")) {
		t.Fatal(n, err)
	}
}

func TestBlockedInboxProbeBudgetReportsRemainingWorkAndEventuallyDiscoversAllUIDs(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	var uids []uint32
	bodies := make(map[uint32]string)
	for uid := uint32(1); uid <= 20; uid++ {
		p := serverBody(t, f, []byte{byte(uid)})
		uids = append(uids, uid)
		bodies[uid] = string(p.MIME())
	}
	m := &inboxInventory{uids: uids, bodies: bodies, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	release := transport.SeedInboxBlockedForTest(inbox, 100, 101)
	defer release()
	more := false
	for sweep := 0; sweep < 3; sweep++ {
		before := 0
		m.mu.Lock()
		for _, n := range m.fetches {
			before += n
		}
		m.mu.Unlock()
		p, err := inbox.Sweep(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		more = more || p.More
		after := 0
		m.mu.Lock()
		for _, n := range m.fetches {
			after += n
		}
		m.mu.Unlock()
		if after-before > 8 || p.Applied != 0 {
			t.Fatal("blocked probe batch exceeded service budget or consumed DATA", p, after-before)
		}
	}
	m.mu.Lock()
	fetched := len(m.fetches)
	m.mu.Unlock()
	if !more || fetched != 20 {
		t.Fatal("bounded probe work omitted inventory", more, fetched)
	}
	if p, err := inbox.Sweep(context.Background()); err != nil || p.More {
		t.Fatal("cached blocked work caused endless rescan", p, err)
	}
	release()
	if p, err := inbox.Sweep(context.Background()); err != nil || p.Applied != 22 {
		t.Fatal("deferred bodies lost replay admission", p, err)
	}
	got := make([]byte, 20)
	n, err := f.client.Read(1, got)
	if err != nil || n != 20 || !bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}) {
		t.Fatal("deferred stream bytes changed", n, err, got)
	}
}

// A queue-admission regression, not a calibrated primary workload. Pending
// application pressure is injected; the later receipt itself crosses the real
// authenticated, separately accounted TLS/IMAP receive path.
func TestFullDataDeliveryQueueStillAllowsAuthenticatedPeerReceipt(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	if err := f.client.Queue(1, []byte("sent bytes"), time.Now()); err != nil {
		t.Fatal(err)
	}
	data := take(t, f.client, transport.Fast, transport.MaxPayload)
	complete(t, data, transport.Accepted)
	if err := f.server.Receive(data.Record); err != nil {
		t.Fatal(err)
	}
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 1<<20, 32)
	publisher, err := transport.NewServerPublisher(f.server, transport.Fast, transport.ControlLedger, f.serverKey, control, f.scope, "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p == nil {
		t.Fatal("receipt not prepared", err)
	}
	origin := f.origin
	origin.Store = control.StoreID()
	origin.Run = control.Run()
	receiver, err := transport.NewClientReceiver(f.client, transport.ControlLedger, f.clientKey, origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{bodies: [][]byte{p.MIME()}, bytes: len(p.MIME())}
	x := box.selected(t, "fast")
	defer x.Close()
	inbox, err := transport.NewClientInbox(x, transport.DefaultInboxLimits(), receiver)
	if err != nil {
		t.Fatal(err)
	}
	release := transport.SeedInboxBlockedForTest(inbox, 7, 8)
	defer release()
	held, err := control.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	progress, err := inbox.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.client.Snapshot().SendBuffered != 0 || progress.Applied != 1 {
		t.Fatal("pending DATA hid the later peer receipt", progress, f.client.Snapshot())
	}
	if after, err := control.Held("account1", "12345678"); err != nil || after != held {
		t.Fatal("peer receipt released physical protocol debt", held, after, err)
	}
	release()
	if progress, err := inbox.Sweep(context.Background()); err != nil || progress.Applied != 2 {
		t.Fatal("retained deliveries lost after pressure cleared", progress, err)
	}
}
