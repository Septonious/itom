package transport_test

import (
	"context"
	"itom.local/core/transport"
	"reflect"
	"testing"
)

func TestContiguousFastReadUsesOneBodyCommandAndUIDGapForcesInventory(t *testing.T) {
	m := &inboxInventory{uids: []uint32{1}, bodies: map[uint32]string{1: "first"}, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	var delivered []string
	inbox := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { delivered = append(delivered, string(b.Bytes)); return nil }), nil
	})
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	m.mu.Lock()
	m.uids = []uint32{1, 2}
	m.bodies[2] = "next"
	m.mu.Unlock()
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	m.mu.Lock()
	metadata, fetched := m.metadata, m.fetches[2]
	m.mu.Unlock()
	if metadata != 1 || fetched != 1 {
		t.Fatal("ordinary new BODY paid an extra metadata command", metadata, fetched)
	}
	m.mu.Lock()
	m.uids = []uint32{1, 2, 4}
	m.bodies[4] = "after gap"
	m.mu.Unlock()
	if p, err := inbox.Poll(context.Background()); err != nil || !p.More || p.Applied != 0 {
		t.Fatal("empty UID prediction did not request inventory", p, err)
	}
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal("UID hole hid a real later BODY", p, err)
	}
	if !reflect.DeepEqual(delivered, []string{"first", "next", "after gap"}) {
		t.Fatal(delivered)
	}
}

func TestSequenceMovementInvalidatesPredictionWithoutInventingDeletionProof(t *testing.T) {
	m := &inboxInventory{uids: []uint32{1, 2}, bodies: map[uint32]string{1: "first", 2: "second"}, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	var delivered []uint32
	inbox := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { delivered = append(delivered, b.UID); return nil }), nil
	})
	if _, err := inbox.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.uids = []uint32{2}
	m.notice = "* 1 EXPUNGE\r\n"
	m.mu.Unlock()
	if p, err := inbox.Poll(context.Background()); err != nil || !p.More || p.Applied != 0 {
		t.Fatal(p, err)
	}
	if x.SequenceVersion() == 0 {
		t.Fatal("sequence deletion did not invalidate the hint")
	}
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 0 {
		t.Fatal("inventory redelivered known UID", p, err)
	}
	m.mu.Lock()
	m.uids = []uint32{2, 3}
	m.bodies[3] = "third"
	m.mu.Unlock()
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal("post-expunge prediction used a stale sequence", p, err)
	}
	if !reflect.DeepEqual(delivered, []uint32{1, 2, 3}) {
		t.Fatal("sequence hint became application data or lost a message", delivered)
	}
}

func TestLateLowerUIDIsFoundWhenBodySequenceRevealsAHole(t *testing.T) {
	m := &inboxInventory{uids: []uint32{1, 3}, bodies: map[uint32]string{1: "first", 3: "third"}, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	var delivered []uint32
	inbox := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { delivered = append(delivered, b.UID); return nil }), nil
	})
	if _, err := inbox.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.uids = []uint32{1, 2, 3, 4}
	m.bodies[2] = "late second"
	m.bodies[4] = "fourth"
	m.mu.Unlock()
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 || !p.More {
		t.Fatal("sequence gap did not request complete inventory", p, err)
	}
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal("late lower UID was skipped", p, err)
	}
	if !reflect.DeepEqual(delivered, []uint32{1, 3, 4, 2}) {
		t.Fatal(delivered)
	}
}
