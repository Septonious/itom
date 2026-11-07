package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestRecoveryFinishesFrozenControlScopeBeforeLiveTail(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	l := &IMAPLink{identity: "frozen-scope", attached: SessionID{1}, reclaimer: NewReclamationCoordinator()}
	folder := l.controlFolder(false)
	rec := seedCollectionFolder(t, p, folder, 300)
	reader := &mailPipe{validity: rec.validity}
	reader.cursor.Store(300)
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], control: reader}
	a.maintenance.epoch = l.attached
	a.maintenance.peerPressure = true
	a.maintenance.inventoried = true
	a.maintenance.remoteStartOut = uidWatermark{rec.validity, 64}
	for range 2 {
		if _, err = a.maintenanceStep(ctx, p, false); err != nil {
			t.Fatal(err)
		}
	}
	if a.maintenance.clearedIn != (uidWatermark{rec.validity, 64}) || a.maintenance.controlMessages != 64 {
		t.Fatal("recovery chased a live tail", a.maintenance.clearedIn, a.maintenance.controlMessages)
	}
	box, err := p.client.Select(folder, nil).Wait()
	if err != nil || box.NumMessages != 236 {
		t.Fatal("live controls removed before old recovery completed", box, err)
	}
}

func TestRecoveryAuditsEmptyOutgoingBoundary(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	l := &IMAPLink{identity: "empty-scope", attached: SessionID{1}, reclaimer: NewReclamationCoordinator()}
	folder := l.controlFolder(true)
	if err = p.client.Create(folder, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	box, err := p.client.Select(folder, nil).Wait()
	if err != nil {
		t.Fatal(err)
	}
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0]}
	m := &a.maintenance
	m.epoch = l.attached
	m.peerPressure = true
	m.inventoried = true
	m.boundaryOut = uidWatermark{box.UIDValidity, 0}
	m.remoteRead = m.boundaryOut
	if _, err = a.maintenanceStep(ctx, p, false); err != nil {
		t.Fatal(err)
	}
	if !covers(m.clearedOut, m.boundaryOut) {
		t.Fatal("empty UID boundary never obtained validity proof", m.clearedOut, m.boundaryOut)
	}
}

func TestRecoveryPreferenceYieldsToOtherCollectionTurns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	l := &IMAPLink{}
	a := &mailAccountState{ctx: ctx, link: l, config: MailAccount{Host: "fixture", Username: "a"}}
	b := &mailAccountState{ctx: ctx, link: l, config: MailAccount{Host: "fixture", Username: "b"}}
	c := NewReclamationCoordinator()
	c.preferRecovery(a, true)
	for range 2 {
		if c.acquire(b) {
			t.Fatal("recovery owner lost its bounded preference")
		}
		if !c.acquire(a) {
			t.Fatal("recovery owner did not progress")
		}
		c.release()
	}
	if c.acquire(a) {
		t.Fatal("recovery preference starved another ready account")
	}
	if !c.acquire(b) {
		t.Fatal("other account did not receive its collection turn")
	}
	c.release()
	c.preferRecovery(a, false)
	if !c.acquire(a) {
		t.Fatal("recovered owner did not return to ordinary FIFO")
	}
	c.release()
}

func TestIdleCollectionClearsBeyondFormerSessionBoundary(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := openMailPipe(ctx, accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	l := &IMAPLink{identity: "idle-frozen", reclaimer: NewReclamationCoordinator()}
	in := seedCollectionFolder(t, p, l.controlFolder(false), 300)
	seedCollectionFolder(t, p, l.controlFolder(true), 300)
	reader := &mailPipe{validity: in.validity}
	reader.cursor.Store(300)
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], control: reader}
	a.maintenance.epoch = SessionID{1}
	a.maintenance.remoteStartOut = uidWatermark{in.validity, 64}
	for range 10 {
		if _, err = a.maintenanceStep(ctx, p, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, folder := range []string{l.controlFolder(false), l.controlFolder(true)} {
		box, err := p.client.Select(folder, nil).Wait()
		if err != nil || box.NumMessages != 0 {
			t.Fatal("idle sweep stopped at former session scope", folder, box, err)
		}
	}
}
