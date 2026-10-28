package transport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Envelope/cache component fixture: authentic outer MAC and exact selected
// scope; no claim that these synthetic packets establish financial authority.
func retainedPacket(t *testing.T, session wire.ID, key []byte) []byte {
	t.Helper()
	k := wire.QuotaKey{Store: wire.ID{2}, Epoch: 1, Grant: 1, Session: session, Account: "a02", User: "12345678"}
	_, b, e := wire.BindServerEnvelope(k, wire.ServerData, wire.ID{3}, "fast", bytes.Repeat([]byte{7}, 64), key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if e != nil {
		t.Fatal(e)
	}
	raw, e := mailbox.Encode(b)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func retainedPeer(t *testing.T, bodies map[uint32][]byte) *Executor {
	t.Helper()
	x, peer := standbyPeer(t)
	x.mailbox, x.validity = "fast", 7
	go func() {
		r := bufio.NewReader(peer)
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				return
			}
			f := strings.Fields(line)
			if strings.Contains(line, "(UID)") {
				uids := make([]uint32, 0, len(bodies))
				for uid := range bodies {
					uids = append(uids, uid)
				}
				sort.Slice(uids, func(a, b int) bool { return uids[a] < uids[b] })
				for _, uid := range uids {
					if bodies[uid] != nil {
						fmt.Fprintf(peer, "* %d FETCH (UID %d)\r\n", uid, uid)
					}
				}
			} else {
				u, _ := strconv.ParseUint(strings.Split(f[3], ":")[0], 10, 32)
				if b := bodies[uint32(u)]; b != nil {
					fmt.Fprintf(peer, "* %d FETCH (UID %d BODY[] {%d}\r\n", u, u, len(b))
					peer.Write(b)
					fmt.Fprint(peer, ")\r\n")
				}
			}
			fmt.Fprintf(peer, "%s OK read\r\n", f[0])
		}
	}()
	return x
}
func TestAuthenticatedRetainedReadHandoffSkipsOnlyHistoricalBodiesInTheSameIncarnation(t *testing.T) {
	key := bytes.Repeat([]byte{17}, 32)
	bodies := map[uint32][]byte{1: retainedPacket(t, wire.ID{77}, key), 2: retainedPacket(t, wire.ID{1}, key)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	inventory := retainedPeer(t, bodies)
	snapshot, e := InspectRetained(ctx, inventory, "12345678", key)
	if e != nil {
		t.Fatal(e)
	}
	service := retainedPeer(t, bodies)
	service.history = &historyScope{user: "12345678", session: wire.ID{1}}
	copy(service.history.key[:], key)
	if e = service.AdoptRetainedInventory(snapshot); e != nil {
		t.Fatal(e)
	}
	var received []uint32
	i, e := NewInbox(service, InboxScope{"a02", "fast", 7}, DefaultInboxLimits(), func(_ uint32, b Body) (InboxDelivery, error) { received = append(received, b.UID); return nil, nil })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = i.Sweep(ctx); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(received, []uint32{2}) || service.Snapshot().Fetch.Started != 1 {
		t.Fatal("historical BODY was read twice or current BODY skipped", received, service.Snapshot())
	}
	// Repeated inventories are not absence proof. A changed incarnation cannot
	// import even one of these otherwise authentic old UID hints.
	changed := retainedPeer(t, bodies)
	changed.validity = 8
	if e = changed.AdoptRetainedInventory(snapshot); e != nil {
		t.Fatal(e)
	}
	if changed.retained != nil {
		t.Fatal("UIDVALIDITY change reused old UID hints")
	}
	foreign := retainedPeer(t, bodies)
	foreign.account = "other"
	if e = foreign.AdoptRetainedInventory(snapshot); e == nil {
		t.Fatal("cross-account cache accepted")
	}
}
func TestRetainedReadHintsRequireFullMACUserFolderAndCurrentAuthenticationKey(t *testing.T) {
	key := bytes.Repeat([]byte{17}, 32)
	raw := retainedPacket(t, wire.ID{77}, key)
	for _, mode := range []string{"bad-mac", "wrong-user", "wrong-folder"} {
		t.Run(mode, func(t *testing.T) {
			x, _ := standbyPeer(t)
			x.mailbox, x.validity = "fast", 7
			user := "12345678"
			k := append([]byte(nil), key...)
			switch mode {
			case "bad-mac":
				k[0]++
			case "wrong-user":
				user = "87654321"
			case "wrong-folder":
				x.mailbox = "bulk"
			}
			x.rememberRetained(Body{UID: 1, Bytes: raw}, user, k)
			if x.retained != nil {
				t.Fatal("unauthenticated or foreign read became a skip hint")
			}
		})
	}
	x, _ := standbyPeer(t)
	x.mailbox, x.validity = "fast", 7
	x.rememberRetained(Body{UID: 1, Bytes: raw}, "12345678", key)
	x.history = &historyScope{user: "12345678", session: wire.ID{1}}
	copy(x.history.key[:], bytes.Repeat([]byte{18}, 32))
	i, e := NewInbox(x, InboxScope{"a02", "fast", 7}, DefaultInboxLimits(), func(uint32, Body) (InboxDelivery, error) { return nil, nil })
	if e != nil {
		t.Fatal(e)
	}
	if len(i.seen) != 0 {
		t.Fatal("old authentication key authorized a current skip")
	}
}
