package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type bootstrapFixture struct {
	client, server *transport.SessionBootstrap
	cx, sx         *transport.Executor
	store          *journal.Store
	wallet         *wallet.Wallet
	binding        secure.Binding
	origin         transport.ServerOrigin
	key            []byte
	box            *sharedMailbox
}

// Only initial CONTROL permission/store identity is supplied by this authority
// fixture. Noise keys are established and confirmed by actual accounted IMAP
// messages, not sessionEpochs or an out-of-band handshaker exchange.
func bootstrapFixtureWithGrant(t *testing.T, grant bool) *bootstrapFixture {
	return bootstrapFixtureWithService(t, grant, nil, nil, false)
}

func bootstrapFixtureWithService(t *testing.T, grant bool, clientService, serverService []time.Duration, idle bool) *bootstrapFixture {
	t.Helper()
	f := &bootstrapFixture{store: issuer(t, filepath.Join(t.TempDir(), "control.db"), 64<<20, 128), key: bytes.Repeat([]byte{17}, 32), box: &sharedMailbox{}}
	var err error
	f.wallet, err = wallet.Open(filepath.Join(t.TempDir(), "control-wallet.db"), "12345678", f.store.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.wallet.Close() })
	f.binding = secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: f.wallet.Session(), Epoch: 1, ConfigHash: [32]byte{5}}
	if _, err = rand.Read(f.binding.Handshake[:]); err != nil {
		t.Fatal(err)
	}
	f.origin = transport.ServerOrigin{Store: f.store.StoreID(), Run: f.store.Run(), Account: "account1", User: f.binding.User, Folder: "fast", UIDValidity: 9}
	if grant {
		acceptBootstrapGrant(t, f)
	}
	f.cx, f.sx = f.box.selectedWithIdleService(t, "fast", clientService, idle, false), f.box.selectedWithIdleService(t, "fast", serverService, idle, false)
	t.Cleanup(func() { f.cx.Close(); f.sx.Close() })
	f.client, err = transport.NewClientSessionBootstrap(f.binding, 7, f.wallet, f.origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.server, err = transport.NewServerSessionBootstrap(f.binding, 7, f.store, "account1", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.client.Close(); f.server.Close() })
	return f
}

func acceptBootstrapGrant(t *testing.T, f *bootstrapFixture) {
	t.Helper()
	g, err := f.store.IssueRemote(journal.Scope{Account: "account1", User: f.binding.User, Session: f.binding.Session}, pool.Amount{Bytes: 32 << 20, Messages: 32})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := f.store.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.wallet.Accept(offer); err != nil {
		t.Fatal(err)
	}
}

func exchangeBootstrap(t *testing.T, client, server *transport.SessionBootstrap, cx, sx *transport.Executor) {
	t.Helper()
	ci, err := client.Inbox(cx, transport.DefaultInboxLimits())
	if err != nil {
		t.Fatal(err)
	}
	si, err := server.Inbox(sx, transport.DefaultInboxLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for step := range 6 {
		sender, x := client, cx
		if step&1 == 1 {
			sender, x = server, sx
		}
		p, err := sender.Prepare(ctx)
		if err != nil || p == nil {
			t.Fatal("bootstrap preparation", step, err)
		}
		if same, err := sender.Prepare(ctx); err != nil || same != p {
			t.Fatal("unstarted bootstrap was reassembled/charged again", step, err)
		}
		if outcome, err := p.Start(ctx, x); err != nil || outcome != transport.Accepted {
			t.Fatal("bootstrap APPEND", step, outcome, err)
		}
		for _, i := range []*transport.Inbox{ci, si} {
			if _, err := i.Sweep(ctx); err != nil {
				t.Fatal("bootstrap scoped inventory", step, err)
			}
		}
		if step < 5 {
			if _, err := client.Cipher(); !errors.Is(err, transport.ErrBootstrapPending) {
				t.Fatal("client keys exposed before mutual proof", step, err)
			}
			if _, err := server.Cipher(); !errors.Is(err, transport.ErrBootstrapPending) {
				t.Fatal("server keys exposed before final publication", step, err)
			}
		}
	}
	if _, err = client.Cipher(); err != nil {
		t.Fatal(err)
	}
	if _, err = server.Cipher(); err != nil {
		t.Fatal(err)
	}
}

func TestAccountedIMAPEstablishesNoiseAndMutualKeyConfirmationWithoutPresetEpochs(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, true)
	exchangeBootstrap(t, f.client, f.server, f.cx, f.sx)
	client, _ := f.client.Cipher()
	server, _ := f.server.Cipher()
	if again, err := f.client.Cipher(); err != nil || again != client {
		t.Fatal("confirmed counter owner was replaced", err)
	}
	record := transport.Record{Kind: transport.Data, Generation: 7, Stream: 1, Payload: []byte("first post-bootstrap record")}
	packet, err := client.Seal(record)
	if err != nil {
		t.Fatal(err)
	}
	got, err := server.Open(packet)
	if err != nil || !bytes.Equal(got.Payload, record.Payload) {
		t.Fatal("confirmed keys cannot carry session records", err)
	}
	entries, err := f.wallet.Entries()
	if err != nil || len(entries) != 1 || entries[0].Used.Messages != 3 || len(entries[0].Pending) != 3 {
		t.Fatal("bootstrap did not preserve exact client charges", entries, err)
	}
	local, remote, err := f.store.HeldByOrigin("account1", f.binding.User)
	if err != nil || local.Messages != 3 || remote.Messages != 32 {
		t.Fatal("bootstrap bypassed local/remote control exposure", local, remote, err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapCannotWriteWithoutRealPermissionAndCanContinueAfterAnActualGrant(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, false)
	if p, err := f.client.Prepare(context.Background()); !errors.Is(err, wallet.ErrExhausted) || p != nil {
		t.Fatal("missing control allowance produced a handshake", p, err)
	}
	if p := f.client.Pending(); p != nil {
		t.Fatal("unfunded bootstrap retained a publication")
	}
	if _, err := f.client.Cipher(); !errors.Is(err, transport.ErrBootstrapPending) {
		t.Fatal(err)
	}
	acceptBootstrapGrant(t, f)
	exchangeBootstrap(t, f.client, f.server, f.cx, f.sx)
}

func TestBootstrapHandoffSurvivesReplicaAbsenceWithoutReplayingHandshakeAsTraffic(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, true)
	exchangeBootstrap(t, f.client, f.server, f.cx, f.sx)
	client, _ := f.client.Cipher()
	server, _ := f.server.Cipher()
	h, err := f.client.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	add(t, a, 1, 1<<20, 1<<20)
	add(t, b, 1, 1<<20, 1<<20)
	r, err := transport.NewClientReceiver(a, transport.ControlLedger, client, f.origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	m := &inboxInventory{bodies: make(map[uint32]string), fetches: make(map[uint32]int)}
	f.box.mu.Lock()
	for i, body := range f.box.bodies {
		m.bodies[uint32(i+1)] = string(body)
	}
	f.box.mu.Unlock()
	x := m.selected(t)
	defer x.Close()
	i, err := transport.NewClientInboxWithBootstrap(x, transport.DefaultInboxLimits(), h, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = i.Sweep(context.Background()); err != nil {
		t.Fatal("replica-absent bootstrap inventory", err)
	}
	if err = b.GrowReceive(1, 2<<20); err != nil {
		t.Fatal(err)
	}
	publisher, err := transport.NewServerPublisher(b, transport.Fast, transport.ControlLedger, server, f.store, journal.Scope{Account: "account1", User: f.binding.User, Session: f.binding.Session}, "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	p, err := publisher.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.uids = []uint32{1, 2, 3, 4, 5, 6, 7}
	m.bodies[7] = string(p.MIME())
	m.mu.Unlock()
	if _, err = i.Sweep(context.Background()); err != nil {
		t.Fatal("handoff lost authenticated bootstrap history", err)
	}
	m.mu.Lock()
	for uid := uint32(1); uid <= 6; uid++ {
		if m.fetches[uid] != 0 {
			t.Error("immutable bootstrap UID fetched/decrypted again", uid)
		}
	}
	m.mu.Unlock()
	flow, _ := a.FlowSnapshot(1)
	if flow.PeerCredit != 2<<20 {
		t.Fatal("new authenticated traffic hidden by bootstrap handoff", flow)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownBootstrapAppendKeepsOneChargeAndRequiresExactStorageProof(t *testing.T) {
	for _, server := range []bool{false, true} {
		name := "client_request"
		if server {
			name = "server_response"
		}
		t.Run(name, func(t *testing.T) {
			f := bootstrapFixtureWithGrant(t, true)
			ctx := context.Background()
			if server {
				p, err := f.client.Prepare(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if outcome, err := p.Start(ctx, f.cx); err != nil || outcome != transport.Accepted {
					t.Fatal(outcome, err)
				}
				i, err := f.server.Inbox(f.sx, transport.DefaultInboxLimits())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = i.Sweep(ctx); err != nil {
					t.Fatal(err)
				}
			}
			owner, old := f.client, f.cx
			if server {
				owner, old = f.server, f.sx
			}
			old.Close() // only this deliberately failed/replaced bootstrap socket
			lost := selectedPeer(t, func(c net.Conn) {
				r := bufio.NewReader(c)
				_, body := readAppendLiteral(t, c, r, readLine(t, r))
				if body == nil {
					return
				}
				f.box.mu.Lock()
				f.box.bodies = append(f.box.bodies, body)
				f.box.bytes += len(body)
				f.box.mu.Unlock()
				// Stored, then EOF before the tagged APPEND reply.
			})
			defer lost.Close()
			p, err := owner.Prepare(ctx)
			if err != nil || p == nil {
				t.Fatal(err)
			}
			if outcome, err := p.Start(ctx, lost); err == nil || outcome != transport.Uncertain {
				t.Fatal("lost reply not retained as unknown", outcome, err)
			}
			entries, err := f.wallet.Entries()
			if err != nil || len(entries) != 1 || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
				t.Fatal(entries, err)
			}
			held, err := f.store.Held("account1", f.binding.User)
			if err != nil {
				t.Fatal(err)
			}
			if next, err := owner.Prepare(ctx); !errors.Is(err, transport.ErrUnknown) || next != nil || owner.Pending() != p {
				t.Fatal("unknown bootstrap was re-prepared", next, err)
			}
			repair := f.box.selected(t, "fast")
			defer repair.Close()
			if _, err = p.Start(ctx, repair); !errors.Is(err, transport.ErrProtocol) {
				t.Fatal("unknown bootstrap got a second writer permission", err)
			}
			f.box.mu.Lock()
			uid := uint32(len(f.box.bodies))
			f.box.mu.Unlock()
			if err = p.ConfirmStored(ctx, repair, uid); err != nil {
				t.Fatal("exact accounted BODY proof did not resolve storage", err)
			}
			if next, err := owner.Prepare(ctx); !errors.Is(err, transport.ErrBootstrapPending) || next != nil {
				t.Fatal("storage proof invented peer key confirmation", err)
			}
			if _, err = owner.Cipher(); !errors.Is(err, transport.ErrBootstrapPending) {
				t.Fatal("own proof enabled data keys", err)
			}
			after, err := f.wallet.Entries()
			if err != nil || len(after) != 1 || after[0].Used != entries[0].Used || len(after[0].Pending) != 1 {
				t.Fatal("storage recovery refunded/reserved bootstrap again", after, err)
			}
			if after, err := f.store.Held("account1", f.binding.User); err != nil || after != held {
				t.Fatal("storage proof refunded control debt", held, after, err)
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBootstrapScopeMismatchCannotAdvanceKeysOrBorrowDataAuthority(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, true)
	p, err := f.client.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := p.Start(context.Background(), f.cx); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	command, err := f.sx.Fetch(context.Background(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	bodies, err := command.Wait(context.Background())
	if err != nil || len(bodies) != 1 {
		t.Fatal(err)
	}
	wrong := f.binding
	wrong.ConfigHash[0] ^= 1
	bad, err := transport.NewServerSessionBootstrap(wrong, 7, f.store, "account1", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err = bad.Receive(f.sx, bodies[0]); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("different profile entered Noise state", err)
	}
	if _, err = bad.Cipher(); !errors.Is(err, transport.ErrBootstrapPending) || bad.Pending() != nil {
		t.Fatal("scope rejection created keys/publication", err)
	}
	data := issuer(t, filepath.Join(t.TempDir(), "data.db"), 4<<20, 32)
	wrongAuthority, err := transport.NewServerSessionBootstrap(f.binding, 7, data, "account1", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	defer wrongAuthority.Close()
	if err = wrongAuthority.Receive(f.sx, bodies[0]); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("control body borrowed DATA ledger authority", err)
	}
	if held, err := data.Held("account1", f.binding.User); err != nil || held != (pool.Amount{}) {
		t.Fatal("wrong ledger mutated", held, err)
	}
	if err = f.server.Receive(f.sx, bodies[0]); err != nil {
		t.Fatal("rejected scope corrupted original physical prefix", err)
	}
	if p, err := f.server.Prepare(context.Background()); err != nil || p == nil {
		t.Fatal("valid original handshake cannot proceed", err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapHandoffRejectsWrongRoleGenerationAndIssuerRun(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, true)
	if _, err := f.client.Handoff(); !errors.Is(err, transport.ErrBootstrapPending) {
		t.Fatal("unconfirmed handoff created", err)
	}
	exchangeBootstrap(t, f.client, f.server, f.cx, f.sx)
	client, _ := f.client.Cipher()
	ch, _ := f.client.Handoff()
	sh, _ := f.server.Handoff()
	for _, owner := range []*transport.SessionBootstrap{f.client, f.server} {
		if _, err := owner.Prepare(context.Background()); !errors.Is(err, transport.ErrClosed) {
			t.Fatal("bootstrap admission remained active after runtime handoff", err)
		}
		if err := owner.Receive(f.cx, transport.Body{}); !errors.Is(err, transport.ErrClosed) {
			t.Fatal("bootstrap reception remained active after runtime handoff", err)
		}
	}
	a := session(t, transport.DefaultLimits())
	r, err := transport.NewClientReceiver(a, transport.ControlLedger, client, f.origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewClientInboxWithBootstrap(f.cx, transport.DefaultInboxLimits(), sh, r); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("server handoff claimed client origin", err)
	}
	otherGeneration, err := transport.NewSession(8, transport.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	rg, err := transport.NewClientReceiver(otherGeneration, transport.ControlLedger, client, f.origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewClientInboxWithBootstrap(f.cx, transport.DefaultInboxLimits(), ch, rg); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("other generation adopted old bootstrap UIDs", err)
	}
	origin := f.origin
	origin.Run++
	rr, err := transport.NewClientReceiver(a, transport.ControlLedger, client, origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transport.NewClientInboxWithBootstrap(f.cx, transport.DefaultInboxLimits(), ch, rr); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("other issuer run adopted bootstrap", err)
	}
	if _, err = transport.NewClientInboxWithBootstrap(f.cx, transport.DefaultInboxLimits(), ch, r); err != nil {
		t.Fatal("failed handoff poisoned valid adoption", err)
	}
}

func TestBootstrapHandoffWaitsForLastOwnBODYInsteadOfAssumingAppendUIDHistory(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, true)
	ci, err := f.client.Inbox(f.cx, transport.DefaultInboxLimits())
	if err != nil {
		t.Fatal(err)
	}
	si, err := f.server.Inbox(f.sx, transport.DefaultInboxLimits())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for step := range 6 {
		sender, x, peer := f.client, f.cx, si
		if step&1 == 1 {
			sender, x, peer = f.server, f.sx, ci
		}
		p, err := sender.Prepare(ctx)
		if err != nil || p == nil {
			t.Fatal(step, err)
		}
		if outcome, err := p.Start(ctx, x); err != nil || outcome != transport.Accepted {
			t.Fatal(step, outcome, err)
		}
		// Read only at the peer: the final server acknowledgement is accepted
		// and peer-confirmed, but has not been observed on its own server route.
		if _, err = peer.Sweep(ctx); err != nil {
			t.Fatal(step, err)
		}
	}
	if _, err = f.server.Cipher(); err != nil {
		t.Fatal("key establishment unexpectedly depended on own UID history", err)
	}
	if _, err = f.server.Handoff(); !errors.Is(err, transport.ErrBootstrapPending) {
		t.Fatal("runtime could rediscover unobserved startup mail as application traffic", err)
	}
	if _, err = si.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.server.Handoff(); err != nil {
		t.Fatal("exact own BODY observation did not complete bounded handoff", err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}
