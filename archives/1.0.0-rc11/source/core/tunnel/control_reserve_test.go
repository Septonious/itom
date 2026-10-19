package tunnel

import (
	"context"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"testing"
	"time"
)

type reservedControlMail struct {
	*memoryMail
	appended chan struct{}
}

func (m *reservedControlMail) Append(ctx context.Context, folder string, raw []byte) error {
	err := m.memoryMail.Append(ctx, folder, raw)
	if err == nil {
		select {
		case m.appended <- struct{}{}:
		default:
		}
	}
	return err
}
func TestCriticalControlPreemptsBlockedNormalWithoutRefund(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	store, err := ledger(t.TempDir(), "control", a, u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.EnableAfterInventory(a.ID, u.ID, store.Run())
	binding := secure.Binding{Server: wire.ID{1}, User: u.ID, Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
	two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
	msg, _ := one.Write(nil)
	if err = two.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, _ = two.Write(nil)
	if err = one.Read(msg); err != nil {
		t.Fatal(err)
	}
	cipher, err := one.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: binding.Session}
	if _, err = store.Issue(scope, pool.Amount{Bytes: 80 * 128, Messages: 80}); err != nil {
		t.Fatal(err)
	}
	m := &reservedControlMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}, appended: make(chan struct{}, 1)}
	m.Ensure(ctx, u.Down)
	l := &lane{ctx: ctx, account: a, user: u, control: store, mail: m, cipher: cipher, sendFolder: u.Down, urgentQueue: make(chan queuedControl, 2)}
	normal, _ := wire.EncodeControl(1, wire.Finish{Final: 1024})
	urgent, _ := wire.EncodeControl(0, wire.Echo{Token: 1})
	l.urgentQueue <- queuedControl{Frame: urgent, added: time.Now()}
	done := make(chan error, 1)
	go func() { done <- l.writeControls([]queuedControl{{Frame: normal, added: time.Now()}}) }()
	select {
	case <-m.appended:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("critical control remained behind blocked normal batch")
	}
	held, err := store.Held(a.ID, u.ID)
	if err != nil || held.Messages != 81 {
		t.Fatal("physical reservation was refunded or exceeded", held, err)
	}
	if err = store.Audit(); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked normal writer did not cancel")
	}
}

func TestMailReceiptMarksCompleteBatchesWithoutPhysicalRefund(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	store, err := ledger(t.TempDir(), "control", a, u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{1}}
	grant, err := store.Issue(scope, pool.Amount{Bytes: 4096, Messages: 3})
	if err != nil {
		t.Fatal(err)
	}
	refs := make([]journal.AttemptRef, 3)
	for i := range refs {
		attempt, err := store.Prepare(grant.Ref, u.Up, wire.ID{byte(i + 1)}, 128, []byte{byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		refs[i] = attempt.Ref
		if _, err = store.Start(attempt.Ref); err != nil {
			t.Fatal(err)
		}
		if err = store.Complete(attempt.Ref, journal.Uncertain); err != nil {
			t.Fatal(err)
		}
	}
	if err = store.CloseGrant(grant.Ref); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Held(a.ID, u.ID)
	l := &lane{obsolete: map[journal.AttemptRef]bool{}, out: []outgoing{{ref: refs[0], counters: []uint64{1, 2}, records: 2}, {ref: refs[1], counters: []uint64{3, 4}, records: 2}, {ref: refs[2], ack: true}}}
	receive := func(counters []uint64) {
		frame, err := wire.EncodeControl(0, wire.MailReceived{Counters: counters})
		if err != nil {
			t.Fatal(err)
		}
		if err = l.process(frame); err != nil {
			t.Fatal(err)
		}
	}
	receive([]uint64{1, 3})
	if len(l.obsolete) != 0 || len(l.out[0].counters) != 1 || len(l.out[1].counters) != 1 {
		t.Fatal("partial ACK marked whole physical message")
	}
	receive([]uint64{1, 3})
	receive([]uint64{2, 4})
	if !l.obsolete[refs[0]] || !l.obsolete[refs[1]] || l.obsolete[refs[2]] {
		t.Fatal("complete ACK or ACK-only message semantics changed", l.obsolete)
	}
	l.registerOutgoingLocked(outgoing{ref: journal.AttemptRef{ID: 99}, counters: []uint64{4}, records: 1})
	if !l.obsolete[journal.AttemptRef{ID: 99}] {
		t.Fatal("ACK before APPEND completion was lost")
	}
	after, err := store.Held(a.ID, u.ID)
	if err != nil || after != before {
		t.Fatal("mail receipt refunded physical debt", before, after, err)
	}
	if err = store.Audit(); err != nil {
		t.Fatal(err)
	}
}
