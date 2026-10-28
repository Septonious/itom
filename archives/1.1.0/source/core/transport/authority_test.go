package transport_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

type authorityFixture struct {
	f                         *serverFixture
	control                   *journal.Store
	dataWallet, controlWallet *wallet.Wallet
	client, server            *transport.AuthorityController
	serverControl             *transport.ServerPublisher
	clientControlReceiver     *transport.ClientReceiver
	serverControlReceiver     *transport.RemoteReceiver
}

// Crypto/scope/authority component fixture. The full cold HTTP scenarios use
// accounted startup rather than this in-memory Noise helper. Only the standing
// initial CONTROL allowance is fixture-supplied here; DATA starts with no offer.
func authorityPair(t *testing.T) *authorityFixture {
	t.Helper()
	f := serverPublications(t, transport.Fast, 64<<20)
	a := &authorityFixture{f: f, control: issuer(t, filepath.Join(t.TempDir(), "control.db"), 64<<20, 128)}
	var err error
	a.dataWallet, err = wallet.OpenBound(filepath.Join(t.TempDir(), "data.db"), f.store.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.dataWallet.Close() })
	a.controlWallet, err = wallet.OpenBound(filepath.Join(t.TempDir(), "control.db"), a.control.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.controlWallet.Close() })
	grant, err := a.control.IssueRemote(f.scope, pool.Amount{Bytes: 32 << 20, Messages: 32})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := a.control.RemoteOffer(grant.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.controlWallet.Accept(offer); err != nil {
		t.Fatal(err)
	}
	a.client, err = transport.NewClientAuthority(f.client, f.clientKey, "account1", transport.ClientAuthorityLedger{Class: transport.DataLedger, Wallet: a.dataWallet, Run: f.store.Run()}, transport.ClientAuthorityLedger{Class: transport.ControlLedger, Wallet: a.controlWallet, Run: a.control.Run()})
	if err != nil {
		t.Fatal(err)
	}
	a.server, err = transport.NewServerAuthority(f.server, f.serverKey, "account1", transport.ServerAuthorityLedger{Class: transport.DataLedger, Store: f.store}, transport.ServerAuthorityLedger{Class: transport.ControlLedger, Store: a.control})
	if err != nil {
		t.Fatal(err)
	}
	a.serverControl, err = transport.NewServerPublisher(f.server, transport.Fast, transport.ControlLedger, f.serverKey, a.control, f.scope, "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	origin := transport.ServerOrigin{Store: a.control.StoreID(), Run: a.control.Run(), Account: "account1", User: "12345678", Folder: "fast", UIDValidity: 9}
	a.clientControlReceiver, err = transport.NewClientReceiver(f.client, transport.ControlLedger, f.clientKey, origin, f.key)
	if err != nil {
		t.Fatal(err)
	}
	a.serverControlReceiver, err = transport.NewRemoteReceiver(f.server, transport.ControlLedger, f.serverKey, a.control, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func authorityRecord(t *testing.T, class transport.LedgerClass, message wire.Control) transport.Record {
	t.Helper()
	frame, err := wire.EncodeControl(0, message)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := frame.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return transport.Record{Kind: transport.Authority, Generation: 7, Offset: 1, Payload: append([]byte{1, byte(class)}, raw...)}
}

// Adversarial peers still reserve and commit ORIGINAL concrete physical quota
// before any test I/O. Neither a forged inner message nor a rejected decode is
// used to fabricate an uncharged/zero-history bootstrap body.
func storedAuthority(t *testing.T, a *authorityFixture, r transport.Record, client bool, physicalClass transport.LedgerClass) (*transport.Executor, transport.Body) {
	t.Helper()
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	plain, err := r.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	n, err := secure.SessionPacketSize(len(plain))
	if err != nil {
		t.Fatal(err)
	}
	var mime []byte
	var localStore *journal.Store
	var localRef journal.AttemptRef
	if client {
		size := func(key wire.QuotaKey, packet int) (uint64, error) {
			body, err := wire.DataEnvelopeSize(key, "fast", packet)
			if err != nil {
				return 0, err
			}
			return mailbox.EncodedSize(body)
		}
		ticket, _, err := a.controlWallet.ReserveSized("account1", n, n, size)
		if err != nil {
			t.Fatal(err)
		}
		packet, err := a.f.clientKey.Seal(r)
		if err != nil {
			t.Fatal(err)
		}
		_, body, err := ticket.Commit("fast", packet, a.f.key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
		if err != nil {
			t.Fatal(err)
		}
		mime, err = mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
	} else {
		store, ledger := a.control, wire.ServerControl
		if physicalClass == transport.DataLedger {
			store, ledger = a.f.store, wire.ServerData
		}
		shape := wire.QuotaKey{Store: store.StoreID(), Epoch: store.Run(), Grant: 1, Account: "account1", User: "12345678", Session: a.f.scope.Session}
		ticket, _, err := store.ReserveLocalSized(a.f.scope, "fast", n, n, func(packet int) (uint64, error) {
			body, err := wire.ServerEnvelopeSize(shape, ledger, "fast", packet)
			if err != nil {
				return 0, err
			}
			return mailbox.EncodedSize(body)
		})
		if err != nil {
			t.Fatal(err)
		}
		packet, err := a.f.serverKey.Seal(r)
		if err != nil {
			t.Fatal(err)
		}
		usage, body, err := wire.BindServerEnvelope(ticket.Key(), ledger, wire.ID{99}, "fast", packet, a.f.key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := ticket.Commit(wire.ID{99}, usage.Attempt.EncodedBytes, body)
		if err != nil {
			t.Fatal(err)
		}
		mime, err = mailbox.Encode(body)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.Audit(); err != nil {
				t.Error(err)
			}
		})
		localStore, localRef = store, attempt.Ref
	}
	cmd, err := x.Append(context.Background(), "fast", mime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if localStore != nil {
		if err := localStore.Complete(localRef, journal.Accepted); err != nil {
			t.Fatal(err)
		}
	}
	return x, fetchAt(t, x, 1)
}

func TestAuthorityRejectsForeignScopeDirectionAndDataLedgerBeforeWalletMutation(t *testing.T) {
	for _, field := range []string{"store", "run", "session", "user", "account", "inner_ledger", "client_grant", "physical_data"} {
		t.Run(field, func(t *testing.T) {
			a := authorityPair(t)
			key := wire.QuotaKey{Store: a.f.store.StoreID(), Epoch: a.f.store.Run(), Grant: 1, Account: "account1", User: "12345678", Session: a.f.scope.Session}
			switch field {
			case "store":
				key.Store[0]++
			case "run":
				key.Epoch++
			case "session":
				key.Session[0]++
			case "user":
				key.User = "87654321"
			case "account":
				key.Account = "account2"
			}
			inner := transport.DataLedger
			if field == "inner_ledger" {
				inner = transport.ControlLedger
			}
			r := authorityRecord(t, inner, wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: 1024, Messages: 1}})
			physical := transport.ControlLedger
			if field == "physical_data" {
				physical = transport.DataLedger
			}
			_, body := storedAuthority(t, a, r, field == "client_grant", physical)
			var err error
			if field == "client_grant" {
				_, err = a.serverControlReceiver.Decode(9, body.UID, body.Bytes)
			} else if field == "physical_data" {
				_, err = a.f.incoming.Decode(9, body.UID, body.Bytes)
			} else {
				_, err = a.clientControlReceiver.Decode(9, body.UID, body.Bytes)
			}
			if !errors.Is(err, transport.ErrProtocol) {
				t.Fatal("invalid authority reached durable handler", err)
			}
			entries, err := a.dataWallet.Entries()
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid grant modified DATA wallet", entries, err)
			}
			if err := a.control.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAuthorityPolicyIssueIsIdempotentAndUnknownMailRetainsExactWork(t *testing.T) {
	a := authorityPair(t)
	ref, err := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 1 << 20, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	if same, err := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 1 << 20, Messages: 8}); err != nil || same != ref {
		t.Fatal("duplicate policy call minted permission", same, err)
	}
	if _, err := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 2 << 20, Messages: 8}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("same request changed allowance", err)
	}
	p, err := a.serverControl.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{reclaimFault: "lost_append_tag"}
	x := box.selected(t, "fast")
	outcome, err := p.Start(context.Background(), x)
	if outcome != transport.Uncertain || err == nil {
		t.Fatal("lost grant mail tag reported accepted", outcome, err)
	}
	if s := a.f.server.Snapshot(); s.AuthorityOutstanding != 1 || s.AuthorityBuffered == 0 || s.ActiveDispatches != 1 {
		t.Fatal("unknown grant work disappeared", s)
	}
	if same, err := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 1 << 20, Messages: 8}); err != nil || same != ref {
		t.Fatal(same, err)
	}
	if again, err := a.serverControl.PrepareControls(context.Background(), transport.MaxControlBytes); err != nil || again != nil {
		t.Fatal("unknown logical grant was re-encrypted/re-APPENDed", err)
	}
	proofExecutor := box.selected(t, "fast")
	proof := fetchAt(t, proofExecutor, 1)
	if err := p.ConfirmStored(context.Background(), proofExecutor, proof.UID); err != nil {
		t.Fatal(err)
	}
	if s := a.f.server.Snapshot(); s.AuthorityOutstanding != 0 || s.AuthorityBuffered != 0 || s.ActiveDispatches != 0 {
		t.Fatal("exact proof did not resolve authority work", s)
	}
	// Own storage proof is NOT permission at the client; only its actual peer
	// decode + durable handler accepts the offer.
	if entries, err := a.dataWallet.Entries(); err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
	d, err := a.clientControlReceiver.Decode(9, proof.UID, proof.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Apply(); err != nil {
		t.Fatal(err)
	}
	entries, err := a.dataWallet.Entries()
	if err != nil || len(entries) != 1 || entries[0].Used.Messages != 0 {
		t.Fatal(entries, err)
	}
}

func TestAuthorityScopeRejectionDoesNotConsumeAValidPacketsReplaySlot(t *testing.T) {
	a := authorityPair(t)
	if _, err := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 1 << 20, Messages: 8}); err != nil {
		t.Fatal(err)
	}
	p, err := a.serverControl.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	if _, err := p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	body := fetchAt(t, x, 1)
	wrong := session(t, transport.DefaultLimits())
	if _, err := transport.NewClientAuthority(wrong, a.f.clientKey, "account1", transport.ClientAuthorityLedger{Class: transport.ControlLedger, Wallet: a.controlWallet, Run: a.control.Run()}, transport.ClientAuthorityLedger{Class: transport.DataLedger, Wallet: a.dataWallet, Run: a.f.store.Run() + 1}); err != nil {
		t.Fatal(err)
	}
	origin := transport.ServerOrigin{Store: a.control.StoreID(), Run: a.control.Run(), Account: "account1", User: "12345678", Folder: "fast", UIDValidity: 9}
	receiver, err := transport.NewClientReceiver(wrong, transport.ControlLedger, a.f.clientKey, origin, a.f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Decode(9, body.UID, body.Bytes); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("stale issuer mapping accepted", err)
	}
	delivery, err := a.clientControlReceiver.Decode(9, body.UID, body.Bytes)
	if err != nil {
		t.Fatal("semantic rejection advanced replay state", err)
	}
	if err := delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	if entries, err := a.dataWallet.Entries(); err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
}

func TestFullAuthorityQueueCannotMintAnotherGrantForTheSamePolicySequence(t *testing.T) {
	a := authorityPair(t)
	limit := pool.Amount{Bytes: 1024, Messages: 1}
	for n := uint64(1); n <= 64; n++ {
		if _, err := a.server.Issue(context.Background(), transport.DataLedger, n, limit); err != nil {
			t.Fatal(n, err)
		}
	}
	ref, err := a.server.Issue(context.Background(), transport.DataLedger, 65, limit)
	if !errors.Is(err, transport.ErrCapacity) || ref.ID == 0 {
		t.Fatal("queue exhaustion lost committed original allocation", ref, err)
	}
	again, err := a.server.Issue(context.Background(), transport.DataLedger, 65, limit)
	if !errors.Is(err, transport.ErrCapacity) || again != ref {
		t.Fatal("retry minted another original allocation", again, err)
	}
	p, err := a.serverControl.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	if _, err := p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	body := fetchAt(t, x, 1)
	delivery, err := a.clientControlReceiver.Decode(9, body.UID, body.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	entries, err := a.dataWallet.Entries()
	if err != nil || len(entries) < 2 || len(entries) > 8 {
		t.Fatal("bounded physical authority batch did not deliver the actual offers", len(entries), err)
	}
	if same, err := a.server.Issue(context.Background(), transport.DataLedger, 65, limit); err != nil || same != ref {
		t.Fatal("retained grant did not queue after real space change", same, err)
	}
	grants, err := a.f.store.ScanGrants(0, 128)
	if err != nil || len(grants) != 65 {
		t.Fatal("queue retry leaked extra grants", len(grants), err)
	}
	if s := a.f.server.Snapshot(); s.AuthorityOutstanding != 65-len(entries) || s.AuthorityOutstanding > 64 || s.AuthorityBuffered == 0 || s.SendBuffered < s.AuthorityBuffered {
		t.Fatal("protocol work escaped bounded session memory", s)
	}
	if err := a.f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorityFenceWaitDoesNotBlockTheSessionActorAndQueuesOnlyOnce(t *testing.T) {
	a := authorityPair(t)
	ref, err := a.server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 1 << 20, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.serverControl.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	if _, err := p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	body := fetchAt(t, x, 1)
	delivery, err := a.clientControlReceiver.Decode(9, body.UID, body.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	entries, err := a.dataWallet.Entries()
	if err != nil || len(entries) != 1 {
		t.Fatal(err)
	}
	key := entries[0].Offer.Key
	ticket, err := a.dataWallet.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.Cancel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fenced := make(chan error, 1)
	go func() { fenced <- a.client.Fence(ctx, transport.DataLedger, key) }()
	awaitReclamation(t, func() bool {
		probe, err := a.dataWallet.Reserve(512)
		if errors.Is(err, wallet.ErrExhausted) {
			return true
		}
		if err != nil {
			t.Fatal(err)
		}
		probe.Cancel()
		return false
	})
	actor := make(chan error, 1)
	go func() { actor <- a.f.client.Queue(1, []byte("independent application work"), time.Now()) }()
	select {
	case err := <-actor:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fence held the stream actor lock")
	}
	if a.client.Stats(transport.DataLedger).AcceptedOffers != 1 {
		t.Fatal("diagnostic snapshot blocked or lost real grant")
	}
	ticket.Cancel()
	select {
	case err := <-fenced:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fence did not follow reservation cancellation")
	}
	if err := a.client.Fence(context.Background(), transport.DataLedger, key); err != nil {
		t.Fatal(err)
	}
	if s := a.f.client.Snapshot(); s.AuthorityOutstanding != 1 {
		t.Fatal("idempotent fence duplicated control work", s)
	}
	if err := a.client.Retire(transport.DataLedger, key); !errors.Is(err, wallet.ErrState) {
		t.Fatal("retirement bypassed durable final cleanup", ref, err)
	}
}

func TestAccountAuthorityRoutesIndependentIssuersWithoutPoisoningPeerReplay(t *testing.T) {
	a := authorityPair(t)
	data := accountIssuer(t, filepath.Join(t.TempDir(), "account2-data.db"), "account2", []string{"fast", "bulk"})
	control := accountIssuer(t, filepath.Join(t.TempDir(), "account2-control.db"), "account2", []string{"fast"})
	dw, err := wallet.OpenBound(filepath.Join(t.TempDir(), "account2-data-wallet.db"), data.StoreID(), a.f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer dw.Close()
	cw, err := wallet.OpenBound(filepath.Join(t.TempDir(), "account2-control-wallet.db"), control.StoreID(), a.f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()
	client, err := transport.NewClientAuthority(a.f.client, a.f.clientKey, "account2", transport.ClientAuthorityLedger{Class: transport.ControlLedger, Wallet: cw, Run: control.Run()}, transport.ClientAuthorityLedger{Class: transport.DataLedger, Wallet: dw, Run: data.Run()})
	if err != nil {
		t.Fatal(err)
	}
	server, err := transport.NewServerAuthority(a.f.server, a.f.serverKey, "account2", transport.ServerAuthorityLedger{Class: transport.ControlLedger, Store: control}, transport.ServerAuthorityLedger{Class: transport.DataLedger, Store: data})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Issue(context.Background(), transport.DataLedger, 1, pool.Amount{Bytes: 1 << 20, Messages: 8}); err != nil {
		t.Fatal(err)
	}
	p, err := a.serverControl.PrepareControls(context.Background(), transport.MaxControlBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	box := &sharedMailbox{}
	x := box.selected(t, "fast")
	if _, err = p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	body := fetchAt(t, x, 1)
	wrong := session(t, transport.DefaultLimits())
	if _, err = transport.NewClientAuthority(wrong, a.f.clientKey, "account2", transport.ClientAuthorityLedger{Class: transport.ControlLedger, Wallet: a.controlWallet, Run: a.control.Run()}, transport.ClientAuthorityLedger{Class: transport.DataLedger, Wallet: a.dataWallet, Run: a.f.store.Run()}); err != nil {
		t.Fatal(err)
	}
	origin := transport.ServerOrigin{Store: a.control.StoreID(), Run: a.control.Run(), Account: "account1", User: "12345678", Folder: "fast", UIDValidity: 9}
	receive, err := transport.NewClientReceiver(wrong, transport.ControlLedger, a.f.clientKey, origin, a.f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = receive.Decode(9, body.UID, body.Bytes); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("foreign account issuer crossed authority map", err)
	}
	delivery, err := a.clientControlReceiver.Decode(9, body.UID, body.Bytes)
	if err != nil {
		t.Fatal("wrong issuer mapping poisoned shared Noise replay", err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	if rows, err := dw.Entries(); err != nil || len(rows) != 1 || rows[0].Offer.Key.Account != "account2" {
		t.Fatal("account2 grant misrouted", rows, err)
	}
	if rows, err := a.dataWallet.Entries(); err != nil || len(rows) != 0 {
		t.Fatal("account1 wallet modified by account2 permission", rows, err)
	}
	if client.Stats(transport.DataLedger).AcceptedOffers != 1 || a.client.Stats(transport.DataLedger).AcceptedOffers != 0 {
		t.Fatal("account counters flattened")
	}
}
