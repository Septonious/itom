package mailstream

import (
	"context"
	"errors"
	imap "github.com/emersion/go-imap/v2"
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
