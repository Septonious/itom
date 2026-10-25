package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/transport"
)

func sizedTestInbox(t *testing.T, x *transport.Executor, batch int, decode transport.InboxDecoder) *transport.Inbox {
	t.Helper()
	limits := transport.DefaultInboxLimits()
	limits.BodyReadBatch = batch
	i, err := transport.NewInbox(x, transport.InboxScope{Account: "account1", Folder: "fast", UIDValidity: 9}, limits, decode)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

// Both variants traverse the same verified TLS/selected parser and an 80ms raw
// TCP propagation route. This measures discovery service, not application RTT,
// receiver-confirmed throughput, independent-host performance or qualification.
func TestSizedDiscoveryReadsSparseReadyMailWithHalfAsManyBodyTurns(t *testing.T) {
	for _, batch := range []int{1, 2} {
		t.Run(fmt.Sprintf("batch_%d", batch), func(t *testing.T) {
			m := &inboxInventory{uids: []uint32{19, 2, 7, 11, 4, 17, 13, 23}, bodies: map[uint32]string{}, fetches: map[uint32]int{}}
			for _, uid := range m.uids {
				m.bodies[uid] = fmt.Sprintf("body-%d", uid)
			}
			x := m.selectedOnNetwork(t, newWireRoute([]time.Duration{80 * time.Millisecond}, time.Second, 62_500_000))
			var applied []uint32
			i := sizedTestInbox(t, x, batch, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
				if string(b.Bytes) != m.bodies[b.UID] {
					return nil, transport.ErrProtocol
				}
				return transport.InboxAction(func() error { applied = append(applied, b.UID); return nil }), nil
			})
			started := time.Now()
			p, err := i.Sweep(context.Background())
			elapsed := time.Since(started)
			if err != nil || p.Applied != 8 || !reflect.DeepEqual(applied, []uint32{2, 4, 7, 11, 13, 17, 19, 23}) {
				t.Fatal("sparse inventory or per-body delivery changed", p, err, applied)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.bodySets) != 8/batch {
				t.Fatal("ready bodies still required individual turns", m.bodySets)
			}
			t.Logf("batch=%d body_commands=%d complete_discovery_ns=%d", batch, len(m.bodySets), elapsed.Nanoseconds())
		})
	}
}

func TestSizedDiscoveryRetainsOrdinalGapsAndDecodedBackpressure(t *testing.T) {
	m := &inboxInventory{uids: []uint32{2, 7, 12}, bodies: map[uint32]string{2: "dependent", 7: "prefix", 12: "blocked"}, fetches: map[uint32]int{}}
	x := m.selected(t)
	prefix, full, decodedBlocked := false, true, 0
	var applied []uint32
	i := sizedTestInbox(t, x, 2, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		if b.UID == 2 && !prefix {
			return nil, journal.ErrOrder // before replay/decryption, as real admission
		}
		if b.UID == 12 {
			decodedBlocked++
		}
		return transport.InboxAction(func() error {
			if b.UID == 12 && full {
				return transport.ErrCapacity
			}
			if b.UID == 7 {
				prefix = true
			}
			applied = append(applied, b.UID)
			return nil
		}), nil
	})
	if p, err := i.Sweep(context.Background()); err != nil || p.Applied != 2 || p.Deferred == 0 {
		t.Fatal(p, err, applied)
	}
	full = false
	if p, err := i.Sweep(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	if decodedBlocked != 1 || !reflect.DeepEqual(applied, []uint32{7, 2, 12}) {
		t.Fatal("a prefetched body bypassed ordering or replay retention", decodedBlocked, applied)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fetches[12] != 1 {
		t.Fatal("blocked decoded BODY was fetched twice", m.fetches)
	}
}

func TestSizedDiscoveryMissingOrUnfittableHintsKeepSingleBodyBounds(t *testing.T) {
	for _, mode := range []string{"missing", "literal_too_large", "aggregate_too_large", "vanished"} {
		t.Run(mode, func(t *testing.T) {
			m := &inboxInventory{uids: []uint32{2, 7}, bodies: map[uint32]string{2: "first", 7: "second"}, fetches: map[uint32]int{}}
			switch mode {
			case "missing":
				m.omitSizes = true
			case "literal_too_large":
				m.sizes = map[uint32]uint32{2: 2<<20 + 1}
			case "aggregate_too_large":
				m.sizes = map[uint32]uint32{2: 2 << 20, 7: 2 << 20}
			case "vanished":
				m.vanish = 2
			}
			x := m.selected(t)
			i := sizedTestInbox(t, x, 2, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
				return transport.InboxAction(func() error { return nil }), nil
			})
			p, err := i.Sweep(context.Background())
			want, turns := 2, 2
			if mode == "vanished" {
				want = 1 // ordinary second pass retries the raced UID separately
			}
			if err != nil || p.Applied != want {
				t.Fatal(p, err)
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.bodySets) != turns {
				t.Fatal("untrusted hints or missing UID changed bounded discovery", m.bodySets)
			}
		})
	}
}

func TestSizedDiscoveryRejectsMalformedSizeMetadata(t *testing.T) {
	for _, size := range []string{"-1", "4294967296", "1 RFC822.SIZE 1"} {
		t.Run(size, func(t *testing.T) {
			x := selectedPeer(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				line := readLine(t, r)
				fmt.Fprintf(c, "* 1 FETCH (UID 1 RFC822.SIZE %s)\r\n%s OK fetched\r\n", size, tag(line))
			})
			i := sizedTestInbox(t, x, 2, func(_ uint32, _ transport.Body) (transport.InboxDelivery, error) {
				t.Error("malformed metadata reached a decoder")
				return nil, nil
			})
			if _, err := i.Sweep(context.Background()); !errors.Is(err, transport.ErrProtocol) {
				t.Fatal("invalid size accepted", err)
			}
		})
	}
}

func TestSizedDiscoveryHintsCannotRaiseActualLiteralOrResponseBounds(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprintf("aggregate_%t", aggregate), func(t *testing.T) {
			x := selectedPeer(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				inventory := readLine(t, r)
				fmt.Fprintf(c, "* 1 FETCH (UID 1 RFC822.SIZE 1)\r\n* 2 FETCH (UID 3 RFC822.SIZE 1)\r\n%s OK fetched\r\n", tag(inventory))
				readLine(t, r)
				if aggregate {
					fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[] {%d}\r\n", 2<<20)
					c.Write(bytes.Repeat([]byte{'x'}, 2<<20))
					fmt.Fprint(c, ")\r\n")
					fmt.Fprintf(c, "* 2 FETCH (UID 3 BODY[] {%d}\r\n", 2<<20)
				} else {
					fmt.Fprintf(c, "* 1 FETCH (UID 1 BODY[] {%d}\r\n", 2<<20+1)
				}
				// No payload is needed after the violating header: the original
				// parser must refuse it before allocating or handing off a BODY.
			})
			i := sizedTestInbox(t, x, 2, func(_ uint32, _ transport.Body) (transport.InboxDelivery, error) {
				t.Error("an over-limit response reached accounting/decryption")
				return nil, nil
			})
			if _, err := i.Sweep(context.Background()); !errors.Is(err, transport.ErrCapacity) {
				t.Fatal("size hints bypassed the unchanged parser bound", err)
			}
		})
	}
}
