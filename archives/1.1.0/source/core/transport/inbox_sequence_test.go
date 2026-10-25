package transport_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"itom.local/core/transport"
)

func TestExactCleanupSequenceChangesKeepContiguousBodyDiscovery(t *testing.T) {
	for _, afterBody := range []bool{false, true} {
		t.Run(fmt.Sprintf("expunge_after_body_%t", afterBody), func(t *testing.T) {
			m := &inboxInventory{uids: []uint32{1, 2}, bodies: map[uint32]string{1: "first", 2: "second", 3: "third", 4: "fourth"}, fetches: map[uint32]int{}}
			x := m.selected(t)
			defer x.Close()
			var applied []uint32
			inbox := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
				return transport.InboxAction(func() error { applied = append(applied, b.UID); return nil }), nil
			})
			if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 2 {
				t.Fatal(p, err)
			}
			m.mu.Lock()
			if afterBody {
				m.uids = []uint32{1, 2, 3}
				m.afterBodyNotice = "* 1 EXPUNGE\r\n"
			} else {
				m.uids = []uint32{2, 3}
				m.notice = "* 1 EXPUNGE\r\n"
			}
			m.mu.Unlock()
			if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 || p.More {
				t.Fatal("exact sequence update forced inventory", p, err)
			}
			m.mu.Lock()
			m.uids = []uint32{2, 3, 4}
			m.mu.Unlock()
			if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 || p.More {
				t.Fatal("post-cleanup new BODY lost its prediction", p, err)
			}
			m.mu.Lock()
			queries := m.metadata
			m.mu.Unlock()
			if queries != 1 || !reflect.DeepEqual(applied, []uint32{1, 2, 3, 4}) {
				t.Fatal("extra metadata or delivery loss", queries, applied)
			}
		})
	}
}

func TestAmbiguousTailDeletionCannotMaskALateLowerUID(t *testing.T) {
	m := &inboxInventory{uids: []uint32{1, 3}, bodies: map[uint32]string{1: "first", 2: "late", 3: "third", 4: "fourth"}, fetches: map[uint32]int{}}
	x := m.selected(t)
	defer x.Close()
	var applied []uint32
	inbox := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { applied = append(applied, b.UID); return nil }), nil
	})
	if _, err := inbox.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A replica exposes lower UID2, moving old UID3 to sequence3, then deletes
	// UID3. The new UID4 now has exactly the ORIGINAL predicted sequence3.
	// Ignoring an EXPUNGE beyond the old observed prefix would hide UID2.
	m.mu.Lock()
	m.uids = []uint32{1, 2, 4}
	m.notice = "* 3 EXPUNGE\r\n"
	m.mu.Unlock()
	if p, err := inbox.Poll(context.Background()); err != nil || !p.More {
		t.Fatal("ambiguous sequence was accepted as complete prefix", p, err)
	}
	if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal("late lower UID was stranded", p, err)
	}
	if !reflect.DeepEqual(applied, []uint32{1, 3, 4, 2}) {
		t.Fatal(applied)
	}
}

func TestSequenceHistoryOverflowAndUnknownCountChangesForceInventory(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprintf("history_overflow_%t", overflow), func(t *testing.T) {
			n := 1
			if overflow {
				n = 40
			}
			m := &inboxInventory{bodies: map[uint32]string{}, fetches: map[uint32]int{}, notice: fmt.Sprintf("* %d EXISTS\r\n", n+1)}
			for index := 1; index <= n; index++ {
				m.uids = append(m.uids, uint32(index))
				m.bodies[uint32(index)] = "known"
			}
			m.bodies[uint32(n+1)] = "next"
			x := m.selected(t)
			defer x.Close()
			inbox := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
				return transport.InboxAction(func() error { return nil }), nil
			})
			if _, err := inbox.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			if overflow {
				m.uids = append([]uint32(nil), m.uids[33:]...)
				m.notice = strings.Repeat("* 1 EXPUNGE\r\n", 33)
			} else {
				m.notice = "* 1 EXISTS\r\n"
			}
			m.uids = append(m.uids, uint32(n+1))
			m.mu.Unlock()
			if p, err := inbox.Poll(context.Background()); err != nil || !p.More {
				t.Fatal("incomplete sequence knowledge did not force inventory", p, err)
			}
			if _, err := inbox.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			m.mu.Lock()
			queries := m.metadata
			m.mu.Unlock()
			if queries != 2 {
				t.Fatal("missing full inventory after uncertain sequence", queries)
			}
		})
	}
}

func TestCleanupPredictionCannotExhaustRememberedUIDsAfterPhysicalMailIsGone(t *testing.T) {
	m := &inboxInventory{uids: []uint32{1}, bodies: map[uint32]string{1: "first"}, fetches: map[uint32]int{}}
	x := m.selected(t)
	defer x.Close()
	limits := transport.DefaultInboxLimits()
	limits.KnownUIDs = 3
	var applied []uint32
	inbox, err := transport.NewInbox(x, transport.InboxScope{Account: "account1", Folder: "fast", UIDValidity: 9}, limits, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { applied = append(applied, b.UID); return nil }), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for uid := uint32(1); uid <= 20; uid++ {
		if uid > 1 {
			m.mu.Lock()
			m.uids = []uint32{uid}
			m.bodies[uid] = "next"
			m.notice = "* 1 EXPUNGE\r\n"
			m.mu.Unlock()
		}
		if p, err := inbox.Poll(context.Background()); err != nil || p.Applied != 1 {
			t.Fatal("discovery cache stopped after ordinary cleanup", uid, p, err)
		}
	}
	if len(applied) != 20 {
		t.Fatal("UIDs were skipped", applied)
	}
}
