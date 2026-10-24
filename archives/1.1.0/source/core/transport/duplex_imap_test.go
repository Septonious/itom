package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Deliberately bounded, uncalibrated protocol fixture. Each endpoint keeps one
// selected executor per plane; both see all bodies, including their own writes.
// There is no production OPEN/discovery, Android frontend, or rate claim here.
type sharedMailbox struct {
	mu      sync.Mutex
	bodies  [][]byte
	bytes   int
	changed chan struct{}
}

func (m *sharedMailbox) selected(t *testing.T, folder string) *transport.Executor {
	t.Helper()
	return selectedPeerOn(t, folder, func(conn net.Conn) {
		r := bufio.NewReader(conn)
		for {
			if _, err := r.Peek(1); err != nil {
				return // executor closed by fixture owner
			}
			line := readLine(t, r)
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == "NOOP" {
				fmt.Fprintf(conn, "%s OK alive\r\n", fields[0])
				continue
			}
			if len(fields) < 3 {
				t.Error("invalid fixture command")
				return
			}
			switch fields[1] {
			case "APPEND":
				_, body := readAppendLiteral(t, conn, r, line)
				if body == nil {
					return
				}
				m.mu.Lock()
				if len(m.bodies) >= 64 || len(body) > (16<<20)-m.bytes {
					m.mu.Unlock()
					t.Error("fixture mailbox bounds exceeded")
					return
				}
				m.bodies = append(m.bodies, body)
				m.bytes += len(body)
				uid := len(m.bodies)
				m.mu.Unlock()
				select {
				case m.changed <- struct{}{}:
				default:
				}
				fmt.Fprintf(conn, "%s OK [APPENDUID 9 %d] stored\r\n", tag(line), uid)
			case "UID":
				var first, last int
				if len(fields) < 5 || fields[2] != "FETCH" {
					t.Error("unsupported fixture UID command")
					return
				}
				if n, err := fmt.Sscanf(fields[3], "%d:%d", &first, &last); err != nil || n != 2 || first < 1 || last < first {
					t.Error("invalid fixture UID interval")
					return
				}
				m.mu.Lock()
				last = min(last, len(m.bodies))
				var bodies [][]byte
				if first <= last {
					bodies = append(bodies, m.bodies[first-1:last]...)
				}
				m.mu.Unlock()
				for i, body := range bodies {
					if fields[4] == "(UID)" {
						fmt.Fprintf(conn, "* %d FETCH (UID %d)\r\n", first+i, first+i)
						continue
					}
					fmt.Fprintf(conn, "* %d FETCH (UID %d BODY[] {%d}\r\n", first+i, first+i, len(body))
					conn.Write(body)
					fmt.Fprint(conn, ")\r\n")
				}
				fmt.Fprintf(conn, "%s OK fetched\r\n", tag(line))
			default:
				t.Error("unsupported fixture command")
				return
			}
		}
	})
}

func TestDuplexTLSIMAPKeepsOriginsLedgersAndPeerReceiptsIndependent(t *testing.T) {
	f := serverPublications(t, transport.Fast, 16<<20)
	control := issuer(t, filepath.Join(t.TempDir(), "protocol.db"), 2<<20, 128)
	stores := map[transport.LedgerClass]*journal.Store{transport.DataLedger: f.store, transport.ControlLedger: control}
	wallets := make(map[transport.LedgerClass]*wallet.Wallet)
	type route struct {
		client *transport.ClientPublisher
		server *transport.ServerPublisher
		up     *transport.RemoteReceiver
		down   *transport.ClientReceiver
		store  *journal.Store
	}
	type service struct {
		box          *sharedMailbox
		client, peer *transport.Executor
		routes       map[transport.LedgerClass]route
		clientCursor uint32
		serverCursor uint32
	}
	for class, store := range stores {
		w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), store.StoreID(), f.clientEpoch)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		wallets[class] = w
		grant, err := store.IssueRemote(f.scope, pool.Amount{Bytes: store.Config().Accounts[0].Limit.Bytes / 2, Messages: 32})
		if err != nil {
			t.Fatal(err)
		}
		offer, err := store.RemoteOffer(grant.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Accept(offer); err != nil {
			t.Fatal(err)
		}
	}
	var services []*service
	for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
		folder := "fast"
		if plane == transport.Bulk {
			folder = "bulk"
		}
		box := &sharedMailbox{}
		s := &service{box: box, client: box.selected(t, folder), peer: box.selected(t, folder), routes: make(map[transport.LedgerClass]route)}
		t.Cleanup(func() { s.client.Close(); s.peer.Close() })
		for _, class := range []transport.LedgerClass{transport.ControlLedger, transport.DataLedger} {
			if plane == transport.Bulk && class == transport.ControlLedger {
				continue
			}
			store := stores[class]
			var r route
			var err error
			r.store = store
			r.client, err = transport.NewClientPublisher(f.client, plane, class, f.clientKey, wallets[class], "account1", folder, f.key)
			if err != nil {
				t.Fatal(err)
			}
			r.server, err = transport.NewServerPublisher(f.server, plane, class, f.serverKey, store, f.scope, folder, f.key)
			if err != nil {
				t.Fatal(err)
			}
			r.up, err = transport.NewRemoteReceiver(f.server, class, f.serverKey, store, "account1", "12345678", folder, 9, f.key)
			if err != nil {
				t.Fatal(err)
			}
			r.down, err = transport.NewClientReceiver(f.client, class, f.clientKey, transport.ServerOrigin{Store: store.StoreID(), Run: store.Run(), Account: "account1", User: "12345678", Folder: folder, UIDValidity: 9}, f.key)
			if err != nil {
				t.Fatal(err)
			}
			s.routes[class] = r
		}
		services = append(services, s)
	}
	upExpected, downExpected := make(map[uint64][]byte), make(map[uint64][]byte)
	upRead, downRead := make(map[uint64]int), make(map[uint64]int)
	for i, id := range []uint64{1, 3, 5} {
		if id != 1 {
			add(t, f.client, id, 1<<20, 1<<20)
			add(t, f.server, id, 1<<20, 1<<20)
		}
		n := (512 << 10) + 123
		if id == 5 {
			n = 32 // cold short work is present alongside sustained demand
		}
		upExpected[id] = bytes.Repeat([]byte{byte(i + 1)}, n)
		downExpected[id] = bytes.Repeat([]byte{byte(i + 10)}, n+17)
		if err := f.client.Queue(id, upExpected[id], time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := f.server.Queue(id, downExpected[id], time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	localRefs := make(map[wire.ID]journal.AttemptRef)
	ownClient, ownServer, peerClient, peerServer := 0, 0, 0, 0
	drained := false
	for round := 0; round < 30; round++ {
		published := 0
		for index, s := range services {
			for _, class := range []transport.LedgerClass{transport.ControlLedger, transport.DataLedger} {
				r, ok := s.routes[class]
				if !ok {
					continue
				}
				var up, down *transport.PreparedPublication
				var err error
				if index == 0 {
					up, err = r.client.Prepare(ctx, transport.MaxPayload)
				} else {
					up, err = r.client.PrepareBulk(ctx, transport.MaxBulkBytes)
				}
				if err != nil {
					t.Fatal(err)
				}
				if index == 0 {
					down, err = r.server.Prepare(ctx, transport.MaxPayload)
				} else {
					down, err = r.server.PrepareBulk(ctx, transport.MaxBulkBytes)
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range []struct {
					body *transport.PreparedPublication
					x    *transport.Executor
				}{{up, s.client}, {down, s.peer}} {
					if p.body == nil {
						continue
					}
					if ref, ok := p.body.ServerAttempt(); ok {
						localRefs[p.body.Usage().Attempt.Batch] = ref
					}
					if outcome, err := p.body.Start(ctx, p.x); err != nil || outcome != transport.Accepted {
						t.Fatal("duplex APPEND failed", outcome, err)
					}
					published++
				}
			}
			for _, end := range []struct {
				x      *transport.Executor
				cursor *uint32
				client bool
			}{{s.client, &s.clientCursor, true}, {s.peer, &s.serverCursor, false}} {
				s.box.mu.Lock()
				last := uint32(len(s.box.bodies))
				s.box.mu.Unlock()
				if *end.cursor == last {
					continue
				}
				command, err := end.x.Fetch(ctx, *end.cursor+1, last)
				if err != nil {
					t.Fatal(err)
				}
				bodies, err := command.Wait(ctx)
				if err != nil || len(bodies) != int(last-*end.cursor) {
					t.Fatal("duplex FETCH failed", err)
				}
				for _, physical := range bodies {
					charge, err := mailbox.CanonicalEncodedBytes(physical.Bytes)
					if err != nil {
						t.Fatal(err)
					}
					body, err := mailbox.Decode(physical.Bytes)
					if err != nil {
						t.Fatal(err)
					}
					serverOrigin := len(body) >= 4 && (string(body[:4]) == "ISD2" || string(body[:4]) == "ISC2")
					var usage wire.UsageReport
					var packet []byte
					if serverOrigin {
						_, usage, packet, err = wire.OpenServerEnvelope(body, f.key, charge)
					} else {
						usage, packet, err = wire.OpenDataEnvelope(body, f.key, charge)
					}
					if err != nil {
						t.Fatal(err)
					}
					var r route
					for _, candidate := range s.routes {
						if candidate.store.StoreID() == usage.Key.Store {
							r = candidate
						}
					}
					if r.store == nil {
						t.Fatal("unscoped physical authority")
					}
					switch {
					case serverOrigin && end.client:
						delivery, err := r.down.Decode(9, physical.UID, physical.Bytes)
						if err != nil {
							t.Fatal(err)
						}
						if err = delivery.Apply(); err != nil {
							t.Fatal(err)
						}
						peerClient++
					case !serverOrigin && !end.client:
						delivery, err := r.up.Decode(9, physical.UID, physical.Bytes)
						if err != nil {
							t.Fatal(err)
						}
						if err = delivery.Apply(); err != nil {
							t.Fatal(err)
						}
						peerServer++
					case serverOrigin:
						if _, err = f.serverKey.OpenRecords(packet); !errors.Is(err, secure.ErrContext) {
							t.Fatal("own server BODY became peer data", err)
						}
						ref, ok := localRefs[usage.Attempt.Batch]
						if !ok {
							t.Fatal("fixture lost local recovery identity")
						}
						if err = r.store.Observe(ref, r.store.Run(), journal.Observation{Account: "account1", Folder: usage.Attempt.Folder, EncodedBytes: charge, Location: journal.Location{UIDValidity: 9, UID: physical.UID}, Decoded: body}); err != nil {
							t.Fatal(err)
						}
						ownServer++
					default:
						if _, err = f.clientKey.OpenRecords(packet); !errors.Is(err, secure.ErrContext) {
							t.Fatal("own client BODY became peer data", err)
						}
						ownClient++ // debt remains in the wallet; this is no GC proof
					}
				}
				*end.cursor = last
			}
		}
		for _, side := range []struct {
			s        *transport.Session
			expected map[uint64][]byte
			read     map[uint64]int
		}{{f.server, upExpected, upRead}, {f.client, downExpected, downRead}} {
			for id, expected := range side.expected {
				got := make([]byte, len(expected)-side.read[id])
				n, err := side.s.Read(id, got)
				if err != nil || !bytes.Equal(got[:n], expected[side.read[id]:side.read[id]+n]) {
					t.Fatal("duplex application bytes differ", id, err)
				}
				side.read[id] += n
			}
		}
		if published == 0 {
			drained = true
			break
		}
	}
	if !drained || f.client.Snapshot().SendBuffered != 0 || f.server.Snapshot().SendBuffered != 0 {
		t.Fatal("authenticated peer receipts did not drain duplex retained data")
	}
	for id := range upExpected {
		if upRead[id] != len(upExpected[id]) || downRead[id] != len(downExpected[id]) {
			t.Fatal("stream stalled with undelivered bytes", id)
		}
	}
	if ownClient == 0 || ownServer == 0 || peerClient != ownServer || peerServer != ownClient {
		t.Fatal("duplex fixture omitted self-reads or peer delivery", ownClient, ownServer, peerClient, peerServer)
	}
	for class, store := range stores {
		if err := store.Audit(); err != nil {
			t.Fatal(err)
		}
		entries, err := wallets[class].Entries()
		if err != nil || len(entries) != 1 || entries[0].Used.Messages == 0 || len(entries[0].Pending) == 0 {
			t.Fatal("peer receipt incorrectly released physical wallet debt", class, err)
		}
	}
}
