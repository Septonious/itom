package transport_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func onlyMailboxBody(t *testing.T, x *transport.Executor) transport.Body {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd, err := x.Enumerate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	uids, err := cmd.Wait(ctx)
	if err != nil || len(uids) != 1 {
		t.Fatal("invitation inventory", len(uids), err)
	}
	cmd, err = x.Fetch(ctx, uids[0].UID, uids[0].UID)
	if err != nil {
		t.Fatal(err)
	}
	bodies, err := cmd.Wait(ctx)
	if err != nil || len(bodies) != 1 || bodies[0].UID != uids[0].UID {
		t.Fatal("actual invitation BODY", len(bodies), err)
	}
	return bodies[0]
}

// The client Session and trusted issuer identity remain authority-fixture
// inputs here. The initial grant now travels in real accounted server mail,
// rather than by calling wallet.Accept directly in the test.
func invitedBootstrapFixture(t *testing.T, clientService, serverService []time.Duration, idle bool) (*bootstrapFixture, *transport.VerifiedInvitation) {
	t.Helper()
	f := bootstrapFixtureWithService(t, false, clientService, serverService, idle)
	template := transport.InvitationTemplate{Binding: f.binding, Generation: 7}
	template.Binding.Handshake = wire.ID{}
	p, err := transport.PrepareSessionInvitation(context.Background(), f.store, template, "account1", "fast", 9, pool.Amount{Bytes: 32 << 20, Messages: 32}, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := p.Start(context.Background(), f.sx); err != nil || outcome != transport.Accepted {
		t.Fatal("accounted initial invitation", outcome, err)
	}
	clientBody, serverBody := onlyMailboxBody(t, f.cx), onlyMailboxBody(t, f.sx)
	clientOffer, err := transport.VerifySessionInvitation(f.cx, clientBody, template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	serverOffer, err := transport.ObserveSessionInvitation(f.store, f.sx, serverBody, template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.client.Close()
	f.server.Close()
	f.client, err = clientOffer.NewClient(f.wallet, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.server, err = serverOffer.NewServer(f.store, f.key)
	if err != nil {
		t.Fatal(err)
	}
	f.binding = clientOffer.Context()
	return f, serverOffer
}

func TestInitialPermissionTravelsAsAccountedMailBeforeIndependentNoiseStartup(t *testing.T) {
	f, offer := invitedBootstrapFixture(t, nil, nil, false)
	entries, err := f.wallet.Entries()
	if err != nil || len(entries) != 1 || entries[0].Used.Messages != 0 || entries[0].Offer.Limit.Messages != 32 {
		t.Fatal("invitation invented spend or altered the original allowance", entries, err)
	}
	if _, err := offer.NewServer(f.store, f.key); !errors.Is(err, transport.ErrInvitationUsed) {
		t.Fatal("second startup owner claimed the same session before its first write", err)
	}
	exchangeDrivenBootstrap(t, f)
	entries, err = f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 3 || len(entries[0].Pending) != 3 {
		t.Fatal("real mail permission did not retain original physical debt", entries, err)
	}
	local, remote, err := f.store.HeldByOrigin("account1", "12345678")
	if err != nil || local.Messages != 4 || remote.Messages != 32 {
		t.Fatal("invitation or key exchange escaped CONTROL authority", local, remote, err)
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestReinstalledWalletCannotTurnAnOldInvitationIntoFreshWriterPermission(t *testing.T) {
	f, oldOffer := invitedBootstrapFixture(t, nil, nil, false)
	exchangeDrivenBootstrap(t, f)
	before, err := f.store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := wallet.Open(filepath.Join(t.TempDir(), "cleared-wallet.db"), "12345678", f.store.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err = oldOffer.NewClient(fresh, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("missing wallet history reset an old original-session grant", err)
	}
	entries, err := fresh.Entries()
	if err != nil || len(entries) != 0 {
		t.Fatal("rejected invitation admitted old permission", entries, err)
	}
	after, err := f.store.Held("account1", "12345678")
	if err != nil || after != before {
		t.Fatal("reinstall inference released original reservations", before, after, err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestInitialInvitationCannotCrossProfileSessionOrActualMailboxScope(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, false)
	template := transport.InvitationTemplate{Binding: f.binding, Generation: 7}
	template.Binding.Handshake = wire.ID{}
	p, err := transport.PrepareSessionInvitation(context.Background(), f.store, template, "account1", "fast", 9, pool.Amount{Bytes: 32 << 10, Messages: 8}, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := p.Start(context.Background(), f.sx); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	body := onlyMailboxBody(t, f.cx)
	for _, field := range []string{"server", "session", "config", "generation"} {
		wrong := template
		switch field {
		case "server":
			wrong.Binding.Server[0]++
		case "session":
			wrong.Binding.Session[0]++
		case "config":
			wrong.Binding.ConfigHash[0]++
		case "generation":
			wrong.Generation++
		}
		if _, err := transport.VerifySessionInvitation(f.cx, body, wrong, f.key); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("invitation crossed trusted binding", field, err)
		}
	}
	other := f.box.selected(t, "bulk")
	defer other.Close()
	if _, err := transport.VerifySessionInvitation(other, body, template, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("invitation crossed the actual selected folder", err)
	}
	verified, err := transport.VerifySessionInvitation(f.cx, body, template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verified.NewServer(f.store, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("client observation stood in for issuer's full BODY proof", err)
	}
}

func TestConcurrentInvitationObserversCannotCreateMultipleStartupOwners(t *testing.T) {
	f := bootstrapFixtureWithGrant(t, false)
	template := transport.InvitationTemplate{Binding: f.binding, Generation: 7}
	template.Binding.Handshake = wire.ID{}
	p, err := transport.PrepareSessionInvitation(context.Background(), f.store, template, "account1", "fast", 9, pool.Amount{Bytes: 32 << 10, Messages: 8}, f.key)
	if err != nil {
		t.Fatal(err)
	}
	if outcome, err := p.Start(context.Background(), f.sx); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	body := onlyMailboxBody(t, f.sx)
	verified, err := transport.ObserveSessionInvitation(f.store, f.sx, body, template, f.key)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	done := make(chan error, 20)
	for range 20 {
		group.Go(func() {
			b, err := verified.NewServer(f.store, f.key)
			if err == nil {
				b.Close()
			}
			done <- err
		})
	}
	group.Wait()
	close(done)
	admitted := 0
	for err := range done {
		if err == nil {
			admitted++
		} else if !errors.Is(err, transport.ErrInvitationUsed) {
			t.Fatal(err)
		}
	}
	if admitted != 1 {
		t.Fatal("one invitation created multiple independent Noise responders", admitted)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}
