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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Deliberately bounded, uncalibrated protocol fixture. Each endpoint keeps one
// selected executor per plane; both see all bodies, including their own writes.
// There is no production OPEN/discovery, Android frontend, or rate claim here.
type sharedMailbox struct {
	mu             sync.Mutex
	bodies         [][]byte
	bytes          int
	peakMessages   int
	changed        chan struct{}
	keywords       bool
	reclamation    bool
	reclaimFault   string // explicit maintenance fault; ordinary traffic is unchanged
	expungeEntered chan struct{}
	expungeRelease <-chan struct{}
	appendEntered  chan struct{}
	appendRelease  <-chan struct{}
	flags          map[uint32]map[string]bool
}

// The fixture accepts explicit sets and ranges, but only walks its bounded
// allocated UID inventory. Sequence numbers count LIVE messages, including
// unrequested intervening UIDs; deleted UIDs are never reassigned.
func fixtureUIDSelection(selector string, allocated int) ([]uint32, error) {
	if len(selector) == 0 || len(selector) > 4096 {
		return nil, fmt.Errorf("invalid bounded fixture UID set")
	}
	type span struct{ first, last uint32 }
	var spans []span
	for _, part := range strings.Split(selector, ",") {
		ends := strings.Split(part, ":")
		if len(ends) > 2 {
			return nil, fmt.Errorf("invalid fixture UID range")
		}
		first, err := strconv.ParseUint(ends[0], 10, 32)
		if err != nil || first == 0 {
			return nil, fmt.Errorf("invalid fixture UID")
		}
		last := first
		if len(ends) == 2 {
			last, err = strconv.ParseUint(ends[1], 10, 32)
			if err != nil || last < first {
				return nil, fmt.Errorf("invalid fixture UID range")
			}
		}
		spans = append(spans, span{uint32(first), uint32(last)})
	}
	var uids []uint32
	for n := 1; n <= allocated; n++ {
		uid := uint32(n)
		for _, span := range spans {
			if uid >= span.first && uid <= span.last {
				uids = append(uids, uid)
				break
			}
		}
	}
	return uids, nil
}

func (m *sharedMailbox) changes() (int, <-chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	return m.liveCountLocked(), m.changed
}

func (m *sharedMailbox) liveCountLocked() int {
	n := 0
	for _, body := range m.bodies {
		if body != nil {
			n++
		}
	}
	return n
}

func (m *sharedMailbox) selected(t *testing.T, folder string) *transport.Executor {
	return m.selectedWithCommandService(t, folder, nil)
}

// Delays bound one command's scripted provider service, including its literal
// continuation where applicable. They are NOT calibrated network RTT/bandwidth,
// independent server CPU/disk load, or a model of concurrent Rambler service.
func (m *sharedMailbox) selectedWithCommandService(t *testing.T, folder string, delays []time.Duration) *transport.Executor {
	return m.selectedWithIdleService(t, folder, delays, false, false)
}

// Notifications travel as real unsolicited EXISTS on the selected connection.
// No test-owned wake channel is exposed to InboxWorker. Silent notifications
// exercise bounded audit discovery rather than an assumed perfect hint path.
func (m *sharedMailbox) selectedWithIdleService(t *testing.T, folder string, delays []time.Duration, idle, silent bool) *transport.Executor {
	t.Helper()
	return m.selectedAccountService(t, "account1", folder, delays, idle, silent)
}

func (m *sharedMailbox) selectedAccountService(t *testing.T, account, folder string, delays []time.Duration, idle, silent bool) *transport.Executor {
	return m.selectedAccountNetworkService(t, account, folder, delays, idle, silent, nil)
}
func (m *sharedMailbox) selectedAccountNetworkService(t *testing.T, account, folder string, delays []time.Duration, idle, silent bool, network transport.NetworkDialer) *transport.Executor {
	t.Helper()
	open := m.prepareAccountService(t, account, folder, delays, idle, silent)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	x, err := open(ctx, network)
	if err != nil {
		t.Fatal(err)
	}
	return x
}
func (m *sharedMailbox) prepareAccountService(t *testing.T, account, folder string, delays []time.Duration, idle, silent bool) func(context.Context, transport.NetworkDialer) (*transport.Executor, error) {
	t.Helper()
	capabilities := ""
	if idle {
		capabilities = "IDLE"
	}
	if m.reclamation {
		capabilities += " UIDPLUS"
	}
	return prepareSelectedAccountPeer(t, account, folder, transport.DefaultIMAPLimits(), capabilities, m.keywords, func(conn net.Conn) {
		r := bufio.NewReader(conn)
		turn := 0
		delay := func() {
			if len(delays) > 0 {
				time.Sleep(delays[turn%len(delays)])
				turn++
			}
		}
		for {
			if _, err := r.Peek(1); err != nil {
				return // executor closed by fixture owner
			}
			line := readLine(t, r)
			delay()
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == "IDLE" && idle {
				count, changed := m.changes()
				if !silent {
					fmt.Fprintf(conn, "* %d EXISTS\r\n", count)
				}
				fmt.Fprint(conn, "+ idling\r\n")
				done := make(chan string, 1)
				go func() { value, _ := r.ReadString('\n'); done <- value }()
				waiting := true
				for waiting {
					select {
					case value := <-done:
						if value == "" {
							return
						}
						if value != "DONE\r\n" {
							t.Error("command before DONE", value)
							return
						}
						delay() // DONE completion has the same scripted service cost
						fmt.Fprintf(conn, "%s OK idle complete\r\n", fields[0])
						waiting = false
					case <-changed:
						count, next := m.changes()
						changed = next
						if !silent {
							fmt.Fprintf(conn, "* %d EXISTS\r\n", count)
						}
					}
				}
				continue
			}
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
				if m.liveCountLocked() >= 64 || len(body) > (16<<20)-m.bytes || len(m.bodies) >= 8192 {
					count, used := m.liveCountLocked(), m.bytes
					m.mu.Unlock()
					t.Errorf("fixture mailbox bounds exceeded: folder=%s messages=%d bytes=%d next=%d", folder, count, used, len(body))
					return
				}
				m.bodies = append(m.bodies, body)
				m.peakMessages = max(m.peakMessages, m.liveCountLocked())
				m.bytes += len(body)
				uid := len(m.bodies)
				if m.changed != nil {
					close(m.changed)
					m.changed = make(chan struct{})
				}
				m.mu.Unlock()
				if uid == 1 && m.appendEntered != nil {
					close(m.appendEntered)
				}
				if uid == 1 && m.appendRelease != nil {
					<-m.appendRelease
				}
				if m.reclaimFault == "lost_append_tag" {
					return
				}
				fmt.Fprintf(conn, "%s OK [APPENDUID 9 %d] stored\r\n", tag(line), uid)
			case "UID":
				if len(fields) < 4 {
					t.Error("invalid fixture UID command")
					return
				}
				m.mu.Lock()
				uids, err := fixtureUIDSelection(fields[3], len(m.bodies))
				m.mu.Unlock()
				if err != nil {
					t.Error(err)
					return
				}
				if len(fields) == 4 && fields[2] == "EXPUNGE" && m.reclamation {
					if m.reclaimFault == "expunge_refused" {
						fmt.Fprintf(conn, "%s NO expunge refused\r\n", tag(line))
						continue
					}
					var removed []int
					m.mu.Lock()
					for _, uid := range uids {
						if m.bodies[uid-1] == nil || !m.flags[uid][`\Deleted`] || m.reclaimFault == "still_visible" || m.reclaimFault == "partial_visible" && uid == 2 {
							continue
						}
						sequence := 0
						for _, body := range m.bodies[:uid] {
							if body != nil {
								sequence++
							}
						}
						m.bytes -= len(m.bodies[uid-1])
						m.bodies[uid-1] = nil
						delete(m.flags, uid)
						removed = append(removed, sequence)
					}
					if len(removed) > 0 && m.changed != nil {
						close(m.changed)
						m.changed = make(chan struct{})
					}
					m.mu.Unlock()
					for _, sequence := range removed {
						fmt.Fprintf(conn, "* %d EXPUNGE\r\n", sequence)
					}
					if m.expungeEntered != nil {
						close(m.expungeEntered)
					}
					if m.expungeRelease != nil {
						<-m.expungeRelease
					}
					if m.reclaimFault == "lost_expunge_tag" {
						return
					}
					if m.reclaimFault == "changed_incarnation" {
						fmt.Fprint(conn, "* OK [UIDVALIDITY 10] reset\r\n")
					}
					fmt.Fprintf(conn, "%s OK exact UID set expunged\r\n", tag(line))
					continue
				}
				if len(fields) == 6 && fields[2] == "STORE" && (fields[4] == "+FLAGS.SILENT" || fields[4] == "-FLAGS.SILENT") && (m.keywords || m.reclamation && fields[5] == `(\Deleted)`) {
					if m.reclaimFault == "store_refused" && fields[5] == `(\Deleted)` {
						fmt.Fprintf(conn, "%s NO store refused\r\n", tag(line))
						continue
					}
					keyword := strings.TrimSuffix(strings.TrimPrefix(fields[5], "("), ")")
					m.mu.Lock()
					if m.flags == nil {
						m.flags = make(map[uint32]map[string]bool)
					}
					for _, uid := range uids {
						if m.bodies[uid-1] == nil {
							continue
						}
						if m.flags[uid] == nil {
							m.flags[uid] = make(map[string]bool)
						}
						if fields[4] == "+FLAGS.SILENT" {
							m.flags[uid][keyword] = true
						} else {
							delete(m.flags[uid], keyword)
						}
					}
					m.mu.Unlock()
					if m.reclaimFault == "lost_store_tag" && fields[5] == `(\Deleted)` {
						return
					}
					fmt.Fprintf(conn, "%s OK flags stored\r\n", tag(line))
					continue
				}
				if len(fields) < 5 || fields[2] != "FETCH" {
					t.Error("unsupported fixture UID command")
					return
				}
				selected := make(map[uint32]bool, len(uids))
				for _, uid := range uids {
					selected[uid] = true
				}
				var replies []transport.Body
				m.mu.Lock()
				sequence := uint32(0)
				for n, body := range m.bodies {
					if body == nil {
						continue
					}
					sequence++
					uid := uint32(n + 1)
					if !selected[uid] {
						continue
					}
					var flags []string
					for flag := range m.flags[uid] {
						flags = append(flags, flag)
					}
					replies = append(replies, transport.Body{UID: uid, Sequence: sequence, Bytes: body, Flags: flags})
				}
				m.mu.Unlock()
				for _, body := range replies {
					if strings.Join(fields[4:], " ") == "(UID FLAGS)" {
						fmt.Fprintf(conn, "* %d FETCH (UID %d FLAGS (%s))\r\n", body.Sequence, body.UID, strings.Join(body.Flags, " "))
						continue
					}
					if fields[4] == "(UID)" {
						fmt.Fprintf(conn, "* %d FETCH (UID %d)\r\n", body.Sequence, body.UID)
						continue
					}
					fmt.Fprintf(conn, "* %d FETCH (UID %d BODY[] {%d}\r\n", body.Sequence, body.UID, len(body.Bytes))
					conn.Write(body.Bytes)
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
