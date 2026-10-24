package transport_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/secure"
	"itom.local/core/transport"
)

// The scripted inventory deliberately includes UID holes, late lower UIDs and
// out-of-order replies. No UIDNEXT/cursor knowledge is given to the worker.
type inboxInventory struct {
	mu      sync.Mutex
	uids    []uint32
	bodies  map[uint32]string
	vanish  uint32
	fetches map[uint32]int
}

func (m *inboxInventory) selected(t *testing.T) *transport.Executor {
	t.Helper()
	return selectedPeerOn(t, "fast", func(c net.Conn) {
		r := bufio.NewReader(c)
		for {
			if _, err := r.Peek(1); err != nil {
				return
			}
			line := readLine(t, r)
			fields := strings.Fields(line)
			if len(fields) != 5 || fields[1] != "UID" || fields[2] != "FETCH" {
				// BODY.PEEK[] is a sixth field in the body request.
				if len(fields) != 6 || fields[1] != "UID" || fields[2] != "FETCH" {
					t.Error("unexpected inbox command", line)
					return
				}
			}
			m.mu.Lock()
			if fields[4] == "(UID)" {
				for index, uid := range m.uids {
					fmt.Fprintf(c, "* %d FETCH (FLAGS () UID %d)\r\n", index+1, uid)
				}
			} else {
				var uid, end uint32
				if n, err := fmt.Sscanf(fields[3], "%d:%d", &uid, &end); err != nil || n != 2 || uid != end {
					m.mu.Unlock()
					t.Error("non-single BODY query")
					return
				}
				m.fetches[uid]++
				if body, ok := m.bodies[uid]; ok && uid != m.vanish {
					fmt.Fprintf(c, "* 1 FETCH (UID %d BODY[] {%d}\r\n%s)\r\n", uid, len(body), body)
				}
			}
			m.mu.Unlock()
			fmt.Fprintf(c, "%s OK fetched\r\n", fields[0])
		}
	})
}

func newTestInbox(t *testing.T, x *transport.Executor, decode transport.InboxDecoder) *transport.Inbox {
	t.Helper()
	i, err := transport.NewInbox(x, transport.InboxScope{Account: "account1", Folder: "fast", UIDValidity: 9}, transport.DefaultInboxLimits(), decode)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestInboxFindsLateLowerUIDAndDefersOrdinalGapBeforeDecrypting(t *testing.T) {
	m := &inboxInventory{uids: []uint32{12, 2, 7}, bodies: map[uint32]string{2: "dependent", 7: "prefix", 12: "last", 4: "late"}, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	var applied []string
	prefix := false
	i := newTestInbox(t, x, func(validity uint32, b transport.Body) (transport.InboxDelivery, error) {
		if validity != 9 {
			t.Fatal("incarnation changed")
		}
		if b.UID == 2 && !prefix {
			return nil, journal.ErrOrder
		}
		return transport.InboxAction(func() error {
			if b.UID == 7 {
				prefix = true
			}
			applied = append(applied, string(b.Bytes))
			return nil
		}), nil
	})
	if p, err := i.Sweep(context.Background()); err != nil || p.Observed != 3 || p.Applied != 3 {
		t.Fatal(p, err)
	}
	// A later inventory exposes an old lower UID. A highest-UID cursor would skip it.
	m.mu.Lock()
	m.uids = []uint32{12, 2, 4, 7}
	m.mu.Unlock()
	if p, err := i.Sweep(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	if !reflect.DeepEqual(applied, []string{"prefix", "last", "dependent", "late"}) {
		t.Fatal(applied)
	}
	select {
	case <-x.Changes():
		t.Fatal("solicited UID enumeration triggered self-polling")
	default:
	}
	for _, scope := range []transport.InboxScope{{Account: "other", Folder: "fast", UIDValidity: 9}, {Account: "account1", Folder: "bulk", UIDValidity: 9}, {Account: "account1", Folder: "fast", UIDValidity: 10}} {
		if _, err := transport.NewInbox(x, scope, transport.DefaultInboxLimits(), func(uint32, transport.Body) (transport.InboxDelivery, error) { return nil, nil }); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("scope mismatch accepted", err)
		}
	}
}

func TestInboxRetainsBackpressuredDeliveryWithoutFetchingOrDecryptingTwice(t *testing.T) {
	m := &inboxInventory{uids: []uint32{4}, bodies: map[uint32]string{4: "authenticated"}, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	decoded, applied := 0, 0
	full := true
	i := newTestInbox(t, x, func(uint32, transport.Body) (transport.InboxDelivery, error) {
		decoded++
		return transport.InboxAction(func() error {
			if full {
				return transport.ErrCapacity
			}
			applied++
			return nil
		}), nil
	})
	for range 3 {
		if _, err := i.Sweep(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	full = false
	if p, err := i.Sweep(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	m.mu.Lock()
	fetched := m.fetches[4]
	m.mu.Unlock()
	if decoded != 1 || fetched != 1 || applied != 1 {
		t.Fatal("replay/backpressure lost single authentication", decoded, fetched, applied)
	}
}

func TestInboxDisappearanceAndReplayNeverInventApplicationDelivery(t *testing.T) {
	m := &inboxInventory{uids: []uint32{3, 8}, bodies: map[uint32]string{3: "vanishing", 8: "seen"}, vanish: 3, fetches: make(map[uint32]int)}
	x := m.selected(t)
	defer x.Close()
	decoded := 0
	i := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		decoded++
		if b.UID != 8 {
			t.Fatal("vanished BODY delivered")
		}
		return nil, secure.ErrReplay
	})
	for range 2 {
		if p, err := i.Sweep(context.Background()); err != nil || p.Applied != 0 {
			t.Fatal(p, err)
		}
	}
	if decoded != 1 {
		t.Fatal("replay repeatedly decoded", decoded)
	}
	// Nothing here has a journal refund/delete API; an empty FETCH is only a race.
}
