package transport_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wire"
)

func rendezvousFixture(t *testing.T, keywords bool, clientService, serverService []time.Duration, idle bool) (*bootstrapFixture, *transport.VerifiedRendezvous, *transport.VerifiedRendezvous) {
	t.Helper()
	f := &bootstrapFixture{store: issuer(t, filepath.Join(t.TempDir(), "control.db"), 64<<20, 128), key: bytes.Repeat([]byte{17}, 32), box: &sharedMailbox{keywords: keywords}}
	f.cx = f.box.selectedWithIdleService(t, "fast", clientService, idle, false)
	f.sx = f.box.selectedWithIdleService(t, "fast", serverService, idle, false)
	t.Cleanup(func() { f.cx.Close(); f.sx.Close() })
	template := transport.InvitationTemplate{Binding: secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Epoch: 1, ConfigHash: [32]byte{5}}, Generation: 7}
	p, err := transport.PrepareRendezvous(context.Background(), f.store, template, "account1", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := p.Start(context.Background(), f.sx); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	client, err := transport.VerifyRendezvous(f.cx, onlyMailboxBody(t, f.cx), template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.ObserveRendezvous(f.store, f.sx, onlyMailboxBody(t, f.sx), template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.wallet, err = client.OpenWallet(filepath.Join(t.TempDir(), "control-wallet.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.wallet.Close() })
	f.binding = template.Binding
	f.binding.Session = f.wallet.Session()
	f.origin = transport.ServerOrigin{Store: f.store.StoreID(), Run: f.store.Run(), Account: "account1", User: "12345678", Folder: "fast", UIDValidity: 9}
	return f, client, server
}

func fetchAt(t *testing.T, x *transport.Executor, uid uint32) transport.Body {
	t.Helper()
	cmd, err := x.Fetch(context.Background(), uid, uid)
	if err != nil {
		t.Fatal(err)
	}
	bodies, err := cmd.Wait(context.Background())
	if err != nil || len(bodies) != 1 || bodies[0].UID != uid {
		t.Fatal("full actual BODY proof", uid, len(bodies), err)
	}
	return bodies[0]
}

func requestedInvitationFixture(t *testing.T, clientService, serverService []time.Duration, idle bool) *bootstrapFixture {
	t.Helper()
	f, client, server := rendezvousFixture(t, true, clientService, serverService, idle)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Submit(ctx, f.cx, f.wallet, f.key); err != nil {
		t.Fatal(err)
	}
	// This fixture starts with exactly one real anchor. Its UID is obtained from
	// actual metadata, never inferred from UIDNEXT or a production high water.
	meta, err := f.sx.Enumerate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uids, err := meta.Wait(ctx)
	if err != nil || len(uids) != 1 {
		t.Fatal(err, uids)
	}
	anchorUID := uids[0].UID
	cmd, err := f.sx.FetchFlags(ctx, anchorUID)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := cmd.Wait(ctx)
	if err != nil || len(flags) != 1 || len(flags[0].Flags) != 1 {
		t.Fatal("actual authenticated request metadata", len(flags), err)
	}
	keyword := flags[0].Flags[0]
	allowance := pool.Amount{Bytes: 32 << 20, Messages: 32}
	p, err := server.PrepareRequestedInvitation(ctx, keyword, allowance, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := server.PrepareRequestedInvitation(ctx, keyword, allowance, f.key); err != nil || again != p {
		t.Fatal("repeated flag allocated another invitation", err)
	}
	if outcome, err := p.Start(ctx, f.sx); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	meta, err = f.sx.Enumerate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uids, err = meta.Wait(ctx)
	if err != nil || len(uids) != 2 {
		t.Fatal(err, len(uids))
	}
	var invitationUID uint32
	for _, m := range uids {
		if m.UID != anchorUID {
			invitationUID = m.UID
		}
	}
	clientOffer, err := client.VerifyInvitation(f.cx, fetchAt(t, f.cx, invitationUID), f.wallet, f.key)
	if err != nil {
		t.Fatal(err)
	}
	serverOffer, err := server.ObserveInvitation(f.sx, fetchAt(t, f.sx, invitationUID), keyword, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.client, err = clientOffer.NewClient(f.wallet, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.server, err = serverOffer.NewServer(f.store, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.client.Close(); f.server.Close() })
	f.binding = clientOffer.Context()
	return f
}

func TestReadOnlyRendezvousAndAuthenticatedMetadataAcquireRealInitialPermission(t *testing.T) {
	f := requestedInvitationFixture(t, nil, nil, false)
	if f.cx.Snapshot().Append.Started != 0 || f.cx.Snapshot().Store.Completed != 1 {
		t.Fatal("client needed an uncharged initial body to obtain permission", f.cx.Snapshot())
	}
	entries, err := f.wallet.Entries()
	if err != nil || len(entries) != 1 || entries[0].Used.Messages != 0 || entries[0].Offer.Key.Session != f.wallet.Session() {
		t.Fatal("actual fresh-session offer not accepted intact", entries, err)
	}
	exchangeDrivenBootstrap(t, f)
	local, remote, err := f.store.HeldByOrigin("account1", "12345678")
	if err != nil || local.Messages != 5 || remote.Messages != 32 {
		t.Fatal("standing anchor, invitation or Noise mail escaped original quota", local, remote, err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRepeatedRendezvousHintCannotAllocateOrPublishAnotherInitialLease(t *testing.T) {
	f, client, server := rendezvousFixture(t, true, nil, nil, false)
	keyword, err := client.RequestKeyword(f.wallet, f.key)
	if err != nil {
		t.Fatal(err)
	}
	allowance := pool.Amount{Bytes: 32 << 10, Messages: 8}
	p, err := server.PrepareRequestedInvitation(context.Background(), keyword, allowance, f.key)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if same, err := server.PrepareRequestedInvitation(context.Background(), keyword, allowance, f.key); err != nil || same != p {
			t.Fatal("repeated routing hint produced a new spend", err)
		}
	}
	// Even a second independently reconstructed observer cannot allocate a new
	// grant for the same original writer Session in this issuer Run.
	template := transport.InvitationTemplate{Binding: f.binding, Generation: 7}
	template.Binding.Session = wire.ID{}
	other, err := transport.ObserveRendezvous(f.store, f.sx, onlyMailboxBody(t, f.sx), template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.PrepareRequestedInvitation(context.Background(), keyword, allowance, f.key); !errors.Is(err, journal.ErrState) {
		t.Fatal("second observer recreated initial permission", err)
	}
	after, err := f.store.Held("account1", "12345678")
	if err != nil || after != before {
		t.Fatal("hint replay altered real quota", before, after, err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRendezvousRequiresNegotiatedKeywordsAndAProfileMACBeforeAnyAllocation(t *testing.T) {
	f, client, server := rendezvousFixture(t, false, nil, nil, false)
	if err := client.Submit(context.Background(), f.cx, f.wallet, f.key); !errors.Is(err, transport.ErrKeywordsUnsupported) {
		t.Fatal("unsupported provider capability was assumed", err)
	}
	if f.cx.Snapshot().Append.Started != 0 || f.cx.Snapshot().Store.Started != 0 {
		t.Fatal("unsupported startup bypassed admission", f.cx.Snapshot())
	}
	keyword, err := client.RequestKeyword(f.wallet, f.key)
	if err != nil {
		t.Fatal(err)
	}
	last := byte('0')
	if keyword[len(keyword)-1] == last {
		last = '1'
	}
	wrong := keyword[:len(keyword)-1] + string(last)
	before, _ := f.store.Held("account1", "12345678")
	if _, err = server.PrepareRequestedInvitation(context.Background(), wrong, pool.Amount{Bytes: 32 << 10, Messages: 8}, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("provider metadata alone created storage permission", err)
	}
	after, _ := f.store.Held("account1", "12345678")
	if before != after {
		t.Fatal("invalid request reserved quota", before, after)
	}
}
