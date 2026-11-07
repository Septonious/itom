package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	imap "github.com/emersion/go-imap/v2"
	"io"
	"net"
	"testing"
	"time"
)

func TestControlMessageReservationsBoundQueuesRecoveryAndUnknownAppend(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, client: true}
	a := &mailAccountState{link: l, ready: true, id: id, fast: make(chan mailWork, 32), bulk: make(chan mailWork, 16), spent: 1 << 20, reserved: 1 << 20}
	l.accounts = []*mailAccountState{a}
	a.maintenance.initialMessages = controlMessageLimit - 1
	a.maintenance.initialOut = uidWatermark{77, 5000}
	packet := header(sealed, id, 9)
	publish := func(uid imap.UID) {
		<-a.fast
		a.mu.Lock()
		a.reserveControlPublicationLocked(1)
		a.mu.Unlock()
		a.controlAppended(&imap.AppendData{UID: uid, UIDValidity: 77}, 128)
	}
	if e := l.SendFeedback(ctx, packet); e != nil {
		t.Fatal(e)
	}
	// Accepted but not yet published packets count too; normal DATA/control stop.
	if e := l.SendFeedback(ctx, packet); !errors.Is(e, ErrCapacity) {
		t.Fatal("queued work escaped limit", e)
	}
	if e := l.Send(ctx, packet, false); !errors.Is(e, ErrCapacity) {
		t.Fatal("ordinary DATA continued on a full control account", e)
	}
	publish(6000)
	for i := 0; i < controlRecoveryMessages; i++ {
		if e := l.SendHousekeeping(ctx, packet); e != nil {
			t.Fatal("bounded recovery reserve unavailable", i, e)
		}
		publish(imap.UID(6001 + i))
	}
	if e := l.SendHousekeeping(ctx, packet); !errors.Is(e, ErrCapacity) {
		t.Fatal("recovery reserve grew without bound", e)
	}
	a.mu.Lock()
	if got := a.controlMessageReservationsLocked(); got != controlMessageLimit+controlRecoveryMessages {
		t.Fatal("reservation count", got)
	}
	if e := a.refundControlLocked(uidWatermark{77, 4999}); e != nil {
		t.Fatal(e)
	}
	if a.maintenance.initialMessages == 0 {
		t.Fatal("partial fence forgot initial remote messages")
	}
	if e := a.refundControlLocked(uidWatermark{77, 10000}); e != nil {
		t.Fatal(e)
	}
	if a.controlMessageReservationsLocked() != 0 {
		t.Fatal("audited prefix did not release reservations")
	}
	// An unknown APPEND is retained even if later prefix cleanup finishes.
	a.controlQueued = 1
	a.reserveControlPublicationLocked(1)
	a.mu.Unlock()
	a.controlAppended(nil, 128)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.controlMessageReservationsLocked() != 1 {
		t.Fatal("unknown APPEND was assumed absent")
	}
}

func TestControlCountRecoveredFromRemoteMailboxAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	name := (&IMAPLink{identity: "control-count-restart", client: true}).controlFolder(true)
	if e = p.client.Create(name, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	var owned imap.UIDSet
	// Seed actual messages, independent of any in-memory or byte-ledger state.
	for i := 0; i < controlMessageLimit+32; i++ {
		cmd, e := p.startAppend(name, []byte("old tiny control message"))
		if e != nil {
			t.Fatal(e)
		}
		d, e := cmd.Wait()
		if e != nil {
			t.Fatal(e)
		}
		owned.AddNum(d.UID)
	}
	for round := 0; round < 2; round++ {
		l, e := NewIMAPLink(ctx, "control-count-restart", accounts[:1], true)
		if e != nil {
			t.Fatal(e)
		}
		waitRetention(t, ctx, func() bool { return l.Snapshot().Ready == 1 }, "control login not ready")
		a := l.accounts[0]
		a.mu.Lock()
		a.id = SessionID{1}
		a.mu.Unlock()
		for i := 0; i < 1000; i++ {
			if e = l.SendFeedback(ctx, header(sealed, SessionID{1}, 9)); !errors.Is(e, ErrCapacity) {
				t.Fatal("restart bypassed remote message cap", round, e)
			}
		}
		line := l.Snapshot().Lines[0]
		if !line.ControlBackpressure || line.ControlMessageReservations != controlMessageLimit+32 {
			t.Fatal("missing backlog diagnostics", line.ControlMessageReservations)
		}
		l.Close()
	}
	status, e := p.client.Status(name, &imap.StatusOptions{NumMessages: true}).Wait()
	if e != nil || status.NumMessages == nil || *status.NumMessages != controlMessageLimit+32 {
		t.Fatal("rejected sends still appended messages", e, status)
	}
	// Prove physical cleanup permits writes again after a fresh startup inventory.
	if _, e = p.client.Select(name, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	p.allowExpunge.Store(true)
	if _, e = p.deleteUIDs(owned, name); e != nil {
		t.Fatal(e)
	}
	p.allowExpunge.Store(false)
	l, e := NewIMAPLink(ctx, "control-count-restart", accounts[:1], true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	waitRetention(t, ctx, func() bool { return l.Snapshot().Ready == 1 }, "clean account did not restart")
	a := l.accounts[0]
	a.mu.Lock()
	a.id = SessionID{1}
	a.mu.Unlock()
	if e = l.SendFeedback(ctx, header(sealed, SessionID{1}, 10)); e != nil {
		t.Fatal("cleaned account did not resume", e)
	}
	waitRetention(t, ctx, func() bool { return l.Snapshot().Appended > 0 }, "accepted packet was not published")
}

// A connected but quiet tunnel must collect a tiny control backlog; a byte
// threshold alone allowed hundreds of small messages to remain indefinitely.
func TestQuietLowByteControlBacklogIsCollectedWhileConnected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	accounts = accounts[:1]
	server, e := NewIMAPLink(ctx, "quiet-control-bound", accounts, false)
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	client, e := NewIMAPLink(ctx, "quiet-control-bound", accounts, true)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	key := make([]byte, 32)
	if _, e = rand.Read(key); e != nil {
		t.Fatal(e)
	}
	go Serve(ctx, server, "quiet-control-bound", key, (&net.Dialer{}).DialContext, nil)
	session, e := Connect(ctx, client, "quiet-control-bound", key)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Close()
	// Complete the same bidirectional DATA proof as the production frontend;
	// a Noise handshake alone intentionally retains bootstrap-only writers.
	echo, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	go func() {
		c, e := echo.Accept()
		if e == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	stream, e := session.OpenTCP(ctx, echo.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	payload := bytes.Repeat([]byte("e"), 16<<10)
	if _, e = stream.Write(payload); e != nil {
		t.Fatal(e)
	}
	got := make([]byte, len(payload))
	if _, e = io.ReadFull(stream, got); e != nil || !bytes.Equal(got, payload) {
		t.Fatal("DATA admission proof failed", e)
	}
	stream.Close()
	waitRetention(t, ctx, func() bool { return server.Snapshot().Writers == 1 }, "server did not activate independent writer")
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	if e = p.client.Create("personal-keep", nil).Wait(); e != nil {
		t.Fatal(e)
	}
	cmd, e := p.startAppend("personal-keep", []byte("Subject: personal\r\n\r\nunrelated user mail"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	folder := client.controlFolder(true)
	for i := 0; i < 40; i++ {
		cmd, e = p.startAppend(folder, encodeMail([]byte("tiny old transport control")))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	waitRetention(t, ctx, func() bool {
		d, e := p.client.Status(folder, &imap.StatusOptions{NumMessages: true}).Wait()
		return e == nil && d.NumMessages != nil && *d.NumMessages < 32
	}, "quiet low-byte control tails did not collect while session remained active")
	if _, e = session.Ping(ctx); e != nil {
		t.Fatal("cleanup interrupted active session", e)
	}
	d, e := p.client.Status("personal-keep", &imap.StatusOptions{NumMessages: true}).Wait()
	if e != nil || d.NumMessages == nil || *d.NumMessages != 1 {
		t.Fatal("automatic collection touched unrelated mail", e, d)
	}
}

func TestInitialMessageCountUsesConservativeAuditedUIDSpan(t *testing.T) {
	a := &mailAccountState{link: &IMAPLink{}}
	a.maintenance.initialOut = uidWatermark{77, 1_000_000}
	a.maintenance.initialMessages = 1000
	if e := a.refundControlLocked(uidWatermark{77, 256}); e != nil {
		t.Fatal(e)
	}
	if a.maintenance.initialMessages != 1000 {
		t.Fatal("UID holes were mistaken for deleted messages")
	}
	if e := a.refundControlLocked(uidWatermark{77, 999_500}); e != nil {
		t.Fatal(e)
	}
	if a.maintenance.initialMessages != 500 {
		t.Fatal("audited upper bound failed to advance")
	}
	if e := a.refundControlLocked(uidWatermark{77, 1_000_000}); e != nil {
		t.Fatal(e)
	}
	if a.maintenance.initialMessages != 0 {
		t.Fatal("full initial fence did not clear")
	}
}

// Peer admission uses a per-account queue, so it must share the same bounded
// reservations as normal traffic even before independent writers are attached.
func TestRouteAnnouncementsReserveBeforePublication(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, client: true}
	a := &mailAccountState{link: l, ready: true, id: id, config: MailAccount{ID: "a01"}, fast: make(chan mailWork, 32), spent: 1 << 20, reserved: 1 << 20}
	l.accounts = []*mailAccountState{a}
	a.maintenance.initialMessages = controlMessageLimit + controlRecoveryMessages - 1
	a.maintenance.initialOut = uidWatermark{77, 5000}
	packet := header(sealed, id, 9)
	if e := l.sendRoute(ctx, id, "a01", packet); e != nil {
		t.Fatal(e)
	}
	if e := l.sendRoute(ctx, id, "a01", packet); !errors.Is(e, ErrCapacity) {
		t.Fatal("route bypassed recovery message bound", e)
	}
	<-a.fast
	a.mu.Lock()
	a.reserveControlPublicationLocked(1)
	a.mu.Unlock()
	a.controlAppended(&imap.AppendData{UID: 6000, UIDValidity: 77}, 128)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.controlQueued != 0 || a.controlMessageReservationsLocked() != controlMessageLimit+controlRecoveryMessages {
		t.Fatal("route publication lost its reservation", a.controlQueued, a.controlMessageReservationsLocked())
	}
}

// Retirement uses actual, length-verified MIME bytes, including headers/base64,
// rather than guessing that an incoming generation with no sender ledger is small.
func TestIncomingGenerationCarriesAuditedMIMEBytes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	accounts, _ := applicationMailServer(t, true, false)
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	const folder = "itom-incoming-mime-proof"
	if e = p.client.Create(folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	total := 0
	for i := 0; i < 128; i++ {
		raw := encodeMail(append(header(sealed, SessionID{1}, uint64(i+1)), bytes.Repeat([]byte("x"), 1024+i)...))
		total += len(raw)
		cmd, e := p.startAppend(folder, raw)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = cmd.Wait(); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = p.selectLog(folder, false); e != nil {
		t.Fatal(e)
	}
	p.rotate = func(context.Context) error { return ErrClosed }
	if e = p.receive(ctx, make(chan []byte, 128)); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
	uids, ok := p.consumed.Nums()
	if !ok || len(uids) != 128 || p.consumedMIMEBytes != total {
		t.Fatal("incoming MIME proof mismatch", len(uids), p.consumedMIMEBytes, total)
	}
	if _, e = p.selectLog(folder, false); e != nil {
		t.Fatal(e)
	}
	if p.consumedMIMEBytes != 0 {
		t.Fatal("new generation reused a previous MIME total")
	}
}

// Cleanup starts with half of the hard count budget left. Once one initial
// prefix is audited, its conservative UID-span refund must yield to DATA again;
// otherwise startup garbage can either monopolize collection or hit the cap.
func TestAuditedInitialMessagePageRestoresDataCollectionHeadroom(t *testing.T) {
	a := &mailAccountState{link: &IMAPLink{}, spent: 1 << 20, reserved: 1 << 20}
	m := &a.maintenance
	m.initialMessages = 1000
	m.initialOut = uidWatermark{77, 1000}
	m.writes = map[imap.UID]controlWrite{}
	for uid := imap.UID(1001); uid <= 1032; uid++ {
		m.writes[uid] = controlWrite{validity: 77, bytes: 100}
	}
	if !a.controlMessagePressureLocked() {
		t.Fatal("initial backlog was left until the hard cap")
	}
	if e := a.refundControlLocked(uidWatermark{77, 256}); e != nil {
		t.Fatal(e)
	}
	if a.controlMessagePressureLocked() || a.spent != 1<<20 {
		t.Fatal("an audited message page monopolized collection or invented byte credit", a.controlMessageReservationsLocked(), a.spent)
	}
}
