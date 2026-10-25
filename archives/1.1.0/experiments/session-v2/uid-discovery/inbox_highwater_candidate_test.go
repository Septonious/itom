package transport_test

import (
 "context"
 "fmt"
 "reflect"
 "testing"
 "itom.local/core/transport"
)

func TestCleanupDoesNotRewindNextUIDAfterEmptyOrShorterInventory(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		t.Run(fmt.Sprintf("retained_prefix_%t", prefix), func(t *testing.T) {
			m := &inboxInventory{uids: []uint32{3}, bodies: map[uint32]string{3: "old"}, fetches: map[uint32]int{}}
			if prefix {
				m.uids = []uint32{1, 3}
				m.bodies[1] = "retained"
			}
			x := m.selected(t)
			var delivered []uint32
			i := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
				return transport.InboxAction(func() error { delivered = append(delivered, b.UID); return nil }), nil
			})
			if _, err := i.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			m.uids = nil
			if prefix {
				m.uids = []uint32{1}
			}
			delete(m.bodies, 3)
			m.mu.Unlock()
			if _, err := i.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			before := m.metadata
			m.uids = append(m.uids, 4)
			m.bodies[4] = "next actual allocation"
			m.mu.Unlock()
			if p, err := i.Poll(context.Background()); err != nil || p.Applied != 1 || p.More {
				t.Fatal("cleanup rewound the next selector and added discovery turns", p, err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if m.metadata != before || m.fetches[4] != 1 || delivered[len(delivered)-1] != 4 {
				t.Fatal("new allocation paid an extra inventory or was lost", m.metadata, m.bodySets, delivered)
			}
		})
	}
}

func TestPreservedNextUIDStillFindsLateLowerMailThroughSequenceGap(t *testing.T) {
	m := &inboxInventory{uids: []uint32{9}, bodies: map[uint32]string{9: "old"}, fetches: map[uint32]int{}}
	x := m.selected(t)
	var delivered []uint32
	i := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { delivered = append(delivered, b.UID); return nil }), nil
	})
	if _, err := i.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.uids = nil
	delete(m.bodies, 9)
	m.mu.Unlock()
	if _, err := i.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.uids = []uint32{3, 10}
	m.bodies[3], m.bodies[10] = "late lower", "new allocation"
	m.mu.Unlock()
	if p, err := i.Poll(context.Background()); err != nil || p.Applied != 1 || !p.More {
		t.Fatal("sequence gap was accepted as a complete prefix", p, err)
	}
	if p, err := i.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal("preserved high-water mark hid lower mail", p, err)
	}
	if !reflect.DeepEqual(delivered, []uint32{9, 10, 3}) {
		t.Fatal(delivered)
	}
}
