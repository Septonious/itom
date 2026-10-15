package tunnel

import (
	"bytes"
	"context"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wire"
)

func TestRev4OldEnvelopeCannotInterruptActivationInControlMailbox(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 1 << 20, Messages: 4})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, 32)
	_, body, err := wire.BindDataEnvelope(offer.Key, 1, wire.ID{5}, 0, u.Up, []byte("opaque encrypted old-session record"), key, canonicalDataSize)
	if err != nil {
		t.Fatal(err)
	}
	mime, err := mailbox.Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	gate, _, err := session.NewGate(session.Server, 1, 0, transportPolicy())
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), gate: gate, recvFolder: u.Up, envelopeKey: key, pending: map[[32]byte]mailbox.Message{}, remoteGC: map[journal.AttemptRef]remoteCleanup{}, reports: map[[32]byte]wire.UsageReport{}, controlQueue: make(chan queuedControl, 8), maintenanceQueue: make(chan queuedControl, 8)}
	row := mailbox.Message{Folder: u.Up, Validity: 1, UID: 10, MIME: mime}
	before, _ := s.Held(a.ID, u.ID)
	if err = l.receiveDataEnvelope(row, body); err != nil {
		t.Fatal("queued old DATA interrupted carrier activation", err)
	}
	status, err := s.RemoteStatus(g.Ref)
	if err != nil || status.Registered.Messages != 0 {
		t.Fatal("unactivated DATA was registered", status, err)
	}
	after, _ := s.Held(a.ID, u.ID)
	if after != before || len(l.pending) != 1 {
		t.Fatal("deferred discovery changed debt or lost BODY", before, after)
	}
	establishRecoveredServer(t, l)
	if err = l.replayBootstrapData(); err != nil {
		t.Fatal("activated recovery did not replay deferred DATA", err)
	}
	status, err = s.RemoteStatus(g.Ref)
	if err != nil || status.Registered.Messages != 1 || len(l.remoteGC) != 1 || len(l.pending) != 0 {
		t.Fatal("old DATA did not reach proof-based cleanup", status, err)
	}
	after, _ = s.Held(a.ID, u.ID)
	if after != before {
		t.Fatal("bootstrap replay forgave unproven physical debt", before, after)
	}
}
