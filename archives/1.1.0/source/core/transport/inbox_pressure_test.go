package transport_test

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestOpenDeferredByProtocolPressureRetainsReplayAndOriginalPhysicalDebt(t *testing.T) {
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	t.Cleanup(func() { a.Close(); b.Close() })
	ce, se := sessionEpochs(t, wire.ID{96})
	ca, err := transport.NewCipher(ce)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := transport.NewCipher(se)
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := transport.NewClientDialer(a, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transport.NewExitServer(b, cb, 256<<10, 1, transport.ExitPolicyFunc(func(context.Context, string, transport.Target) (netip.AddrPort, error) {
		return netip.AddrPort{}, transport.OpenDenied
	}), &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 1<<20, 32)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), control.StoreID(), ce)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{96}}
	g, err := control.IssueRemote(scope, pool.Amount{Bytes: 1 << 20, Messages: 16})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := control.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{27}, 32)
	publisher, err := transport.NewClientPublisher(a, transport.Fast, transport.ControlLedger, ca, w, "account1", "fast", key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		conn, err := dialer.DialContext(ctx, "tcp", "example.test:443")
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()
	defer func() { cancel(); <-done }()
	for {
		wake := a.WorkChanged()
		if a.ReadyFor(transport.Fast, transport.ControlLedger) {
			break
		}
		select {
		case <-wake:
		case <-time.After(time.Second):
			t.Fatal("OPEN not queued")
		}
	}
	p, err := publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	r, err := transport.NewRemoteReceiver(b, transport.ControlLedger, cb, control, "account1", "12345678", "fast", 9, key)
	if err != nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{bodies: [][]byte{p.MIME()}, bytes: len(p.MIME())}
	x := box.selected(t, "fast")
	defer x.Close()
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), r)
	if err != nil {
		t.Fatal(err)
	}
	release := transport.SeedInboxBlockedControlsForTest(inbox, 7, 8)
	defer release()
	if p, err := inbox.Sweep(context.Background()); err != nil || p.Applied != 0 || b.Snapshot().Streams != 0 {
		t.Fatal("OPEN bypassed full bounded protocol admission", p, err)
	}
	grant, err := control.GetGrant(g.Ref)
	if err != nil || grant.Registered.Messages != 1 {
		t.Fatal("authenticated physical observation lost on deferred OPEN", grant, err)
	}
	held, err := control.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if progress, err := inbox.Sweep(context.Background()); err != nil || progress.Applied != 3 || b.Snapshot().Streams != 1 {
		t.Fatal("deferred OPEN consumed replay or was lost", progress, err, b.Snapshot())
	}
	if after, err := control.Held("account1", "12345678"); err != nil || after != held {
		t.Fatal("OPEN admission changed physical exposure", held, after, err)
	}
}

func TestFullProtocolDeliveryQueueCannotHideResetOfExistingStream(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 1<<20, 32)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), control.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := control.IssueRemote(f.scope, pool.Amount{Bytes: 1 << 20, Messages: 16})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := control.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	conn, err := f.client.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	publisher, err := transport.NewClientPublisher(f.client, transport.Fast, transport.ControlLedger, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p == nil {
		t.Fatal("reset not prepared", err)
	}
	r, err := transport.NewRemoteReceiver(f.server, transport.ControlLedger, f.serverKey, control, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{bodies: [][]byte{p.MIME()}, bytes: len(p.MIME())}
	x := box.selected(t, "fast")
	defer x.Close()
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), r)
	if err != nil {
		t.Fatal(err)
	}
	release := transport.SeedInboxBlockedControlsForTest(inbox, 7, 8)
	defer release()
	progress, err := inbox.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flow, ok := f.server.FlowSnapshot(1)
	if !ok || !flow.Reset || f.server.Snapshot().ReceiveReserved != 0 || progress.Applied != 1 {
		t.Fatal("blocked protocol deliveries hid freeing RESET", progress, flow, f.server.Snapshot())
	}
	entries, err := w.Entries()
	if err != nil || len(entries) != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("reset invented physical reconciliation", entries, err)
	}
}

func TestBoundedProbeContinuationFindsMissingOrdinalInsteadOfRepeatingNewestUIDs(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 1<<20, 64)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), control.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := control.IssueRemote(f.scope, pool.Amount{Bytes: 1 << 20, Messages: 32})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := control.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	publisher, err := transport.NewClientPublisher(f.client, transport.Fast, transport.ControlLedger, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	m := &inboxInventory{bodies: make(map[uint32]string), fetches: make(map[uint32]int)}
	for uid := uint32(1); uid <= 20; uid++ {
		if err = f.server.Queue(1, []byte{byte(uid)}, time.Now()); err != nil {
			t.Fatal(err)
		}
		data := take(t, f.server, transport.Fast, transport.MaxPayload)
		complete(t, data, transport.Accepted)
		if err = f.client.Receive(data.Record); err != nil {
			t.Fatal(err)
		}
		p, err := publisher.Prepare(context.Background(), transport.MaxPayload)
		if err != nil || p == nil {
			t.Fatal(err)
		}
		m.uids = append(m.uids, uid)
		m.bodies[uid] = string(p.MIME())
	}
	r, err := transport.NewRemoteReceiver(f.server, transport.ControlLedger, f.serverKey, control, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	x := m.selected(t)
	defer x.Close()
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), r)
	if err != nil {
		t.Fatal(err)
	}
	release := transport.SeedInboxBlockedForTest(inbox, 100, 101)
	defer release()
	for range 10 {
		p, err := inbox.Sweep(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !p.More {
			break
		}
	}
	if state := f.server.Snapshot(); state.SendBuffered != 0 {
		t.Fatal("probe continuation never reached missing oldest ordinal", state)
	}
	m.mu.Lock()
	total := 0
	for _, n := range m.fetches {
		total += n
	}
	m.mu.Unlock()
	if total > 24 {
		t.Fatal("dependency discovery repeatedly scanned newest BODYs", total)
	}
}

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
	testReceiptPastPressure(t, transport.SeedInboxBlockedForTest)
}

func TestFullProtocolDeliveryQueueStillAllowsAuthenticatedPeerReceipt(t *testing.T) {
	testReceiptPastPressure(t, transport.SeedInboxBlockedControlsForTest)
}

func testReceiptPastPressure(t *testing.T, seed func(*transport.Inbox, ...uint32) func()) {
	t.Helper()
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
	release := seed(inbox, 7, 8)
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
