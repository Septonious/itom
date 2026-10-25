package transport_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func exchangeDrivenRoster(t *testing.T, f *bootstrapFixture, ch, sh *transport.BootstrapHandoff, data *journal.Store, bulkClient, bulkServer *transport.Executor) (*transport.RosterResult, *transport.RosterResult) {
	t.Helper()
	plan := transport.RosterPlan{Accounts: []transport.RosterAccount{{Account: "account1", FastFolder: "fast", BulkFolder: "bulk"}}, ClientBudget: transport.RuntimeBudget{Connections: 2, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 3, PerAccount: 3}}
	return exchangeSelectedRoster(t, f, ch, sh, data, plan, []transport.RosterLane{{Plane: transport.Fast, Executor: f.cx}, {Plane: transport.Bulk, Executor: bulkClient}}, []transport.RosterLane{{Plane: transport.Fast, Executor: f.sx}, {Plane: transport.Bulk, Executor: bulkServer}})
}

func exchangeSelectedRoster(t *testing.T, f *bootstrapFixture, ch, sh *transport.BootstrapHandoff, data *journal.Store, plan transport.RosterPlan, clientLanes, serverLanes []transport.RosterLane) (*transport.RosterResult, *transport.RosterResult) {
	t.Helper()
	client, err := transport.NewClientRoster(ch, plan, clientLanes, f.wallet, filepath.Join(t.TempDir(), "data-wallet.db"), f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	server, err := transport.NewServerRoster(sh, plan, serverLanes, f.store, data, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		client bool
		r      *transport.RosterResult
		err    error
	}
	done := make(chan result, 2)
	go func() { r, err := server.Run(ctx); done <- result{false, r, err} }()
	go func() { r, err := client.Run(ctx); done <- result{true, r, err} }()
	var cr, sr *transport.RosterResult
	for range 2 {
		r := <-done
		if r.err != nil {
			t.Fatal("independent roster", r.client, r.err, client.Snapshot(), server.Snapshot())
		}
		if r.client {
			cr = r.r
		} else {
			sr = r.r
		}
	}
	for _, d := range []*transport.RosterDriver{client, server} {
		s := d.Snapshot()
		own, peer := 2, 1
		if s.Client {
			own, peer = 1, 2
		}
		if !s.RosterConfirmed || s.SelectedAccounts != len(plan.Accounts) || s.OwnObserved != own || s.PeerObserved != peer || s.Prepared != uint64(own) || s.Accepted != uint64(own) || s.Uncertain != 0 || s.ElapsedNS <= 0 || s.Failure != "" {
			t.Fatal("roster diagnostics contradict actual exchange", s)
		}
	}
	if cr.Manifest().DataStore != data.StoreID() || cr.Manifest().DataRun != data.Run() || cr.DataWallet().Identity().Store != data.StoreID() {
		t.Fatal("roster did not convey actual original DATA identity")
	}
	entries, err := cr.DataWallet().Entries()
	if err != nil || len(entries) != 0 {
		t.Fatal("roster invented DATA permission")
	}
	client.Close()
	server.Close()
	for _, l := range append(append([]transport.RosterLane(nil), clientLanes...), serverLanes...) {
		x := l.Executor
		cmd, err := x.Noop(ctx)
		if err != nil {
			t.Fatal("roster closed transferred carrier", err)
		}
		if _, err = cmd.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
	return cr, sr
}

// These fixtures use actual accounted Noise startup and selected TLS/IMAP
// sockets. They isolate startup failure semantics, not provider performance.
func rosterFailureFixture(t *testing.T) (*bootstrapFixture, *transport.RosterDriver, *transport.RosterDriver, *transport.Executor, *transport.Executor, string) {
	t.Helper()
	return rosterFixtureWithPlans(t, nil)
}

func rosterFixtureWithPlans(t *testing.T, customize func(*transport.RosterPlan)) (*bootstrapFixture, *transport.RosterDriver, *transport.RosterDriver, *transport.Executor, *transport.Executor, string) {
	t.Helper()
	f := requestedInvitationFixture(t, nil, nil, false)
	ch, sh := exchangeDrivenBootstrap(t, f)
	box := &sharedMailbox{reclamation: true, keywords: true}
	cx, sx := box.selectedWithIdleService(t, "bulk", nil, false, false), box.selectedWithIdleService(t, "bulk", nil, false, false)
	t.Cleanup(func() { cx.Close(); sx.Close() })
	data := issuer(t, filepath.Join(t.TempDir(), "data.db"), 64<<20, 128)
	plan := transport.RosterPlan{Accounts: []transport.RosterAccount{{Account: "account1", FastFolder: "fast", BulkFolder: "bulk"}}, ClientBudget: transport.RuntimeBudget{Connections: 2, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 3, PerAccount: 3}}
	path := filepath.Join(t.TempDir(), "data-wallet.db")
	clientPlan := plan
	if customize != nil {
		customize(&clientPlan)
	}
	c, err := transport.NewClientRoster(ch, clientPlan, []transport.RosterLane{{Plane: transport.Fast, Executor: f.cx}, {Plane: transport.Bulk, Executor: cx}}, f.wallet, path, f.key)
	if err != nil {
		t.Fatal(err)
	}
	s, err := transport.NewServerRoster(sh, plan, []transport.RosterLane{{Plane: transport.Fast, Executor: f.sx}, {Plane: transport.Bulk, Executor: sx}}, f.store, data, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(); s.Close() })
	return f, c, s, cx, sx, path
}

func TestRosterCancellationPreservesSelectedLanesAndResumesOriginalExchange(t *testing.T) {
	f, c, s, cx, sx, _ := rosterFailureFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.Run(ctx); done <- err }()
	deadline := time.After(3 * time.Second)
	for c.Snapshot().DiscoveryTurns == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("roster never inspected actual mailbox")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := c.Run(context.Background()); !errors.Is(err, transport.ErrProtocol) {
		cancel()
		t.Fatal("concurrent owner accepted", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if state := c.Snapshot(); state.RosterConfirmed || state.Prepared != 0 || state.Failure != "canceled" {
		t.Fatal("cancellation invented roster acceptance", state)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() { _, err := s.Run(ctx); done <- err }()
	r, err := c.Run(ctx)
	if err != nil {
		t.Fatal(err, c.Snapshot(), s.Snapshot())
	}
	defer r.DataWallet().Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	c.Close()
	s.Close()
	for _, x := range []*transport.Executor{f.cx, f.sx, cx, sx} {
		cmd, err := x.Noop(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = cmd.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRosterLostAppendReplyKeepsExactChargedPublicationAndCannotBecomeReady(t *testing.T) {
	f, _, s, _, _, _ := rosterFailureFixture(t)
	f.box.mu.Lock()
	f.box.reclaimFault = "lost_append_tag"
	f.box.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.Run(ctx); !errors.Is(err, transport.ErrUnknown) {
		t.Fatal("ambiguous write was not retained", err)
	}
	pending := s.Pending()
	if pending == nil {
		t.Fatal("unknown publication lost")
	}
	before := s.Snapshot()
	if before.Prepared != 1 || before.Uncertain != 1 || before.RosterConfirmed || before.OwnObserved != 0 {
		t.Fatal(before)
	}
	if _, err := s.Run(ctx); !errors.Is(err, transport.ErrUnknown) {
		t.Fatal("unknown write resumed without actual proof", err)
	}
	after := s.Snapshot()
	if s.Pending() != pending || after.Prepared != before.Prepared || after.IMAP.Append.Started != before.IMAP.Append.Started {
		t.Fatal("unknown roster was resent or rebuilt", before, after)
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRosterForeignExistingWalletIsRetainedAndNeverAcknowledged(t *testing.T) {
	f, c, s, _, _, path := rosterFailureFixture(t)
	epoch, err := f.client.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	foreign := wire.ID{99}
	w, err := wallet.OpenBound(path, foreign, epoch)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Run(ctx); done <- err }()
	if _, err = c.Run(ctx); !errors.Is(err, wallet.ErrState) {
		t.Fatal("foreign issuer identity was silently replaced", err)
	}
	if state := c.Snapshot(); state.Prepared != 0 || state.RosterConfirmed {
		t.Fatal("wallet mismatch still sent ACCEPT", state)
	}
	cancel()
	<-done
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("rejected wallet was modified", err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRosterTenSelectedAccountsAgreeOnActualIncarnationsWithinPhoneServerBudgets(t *testing.T) {
	var extra []string
	for n := 2; n <= 10; n++ {
		extra = append(extra, fmt.Sprintf("account%d", n))
	}
	f := requestedInvitationFixture(t, nil, nil, false, extra...)
	ch, sh := exchangeDrivenBootstrap(t, f)
	data := issuer(t, filepath.Join(t.TempDir(), "data.db"), 64<<20, 128, extra...)
	plan := transport.RosterPlan{ClientBudget: transport.RuntimeBudget{Connections: 20, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 30, PerAccount: 3}}
	var clientLanes, serverLanes []transport.RosterLane
	for _, account := range append([]string{"account1"}, extra...) {
		plan.Accounts = append(plan.Accounts, transport.RosterAccount{Account: account, FastFolder: "fast", BulkFolder: "bulk"})
		for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
			folder := "fast"
			if plane == transport.Bulk {
				folder = "bulk"
			}
			var cx, sx *transport.Executor
			if account == "account1" && plane == transport.Fast {
				cx, sx = f.cx, f.sx
			} else {
				box := &sharedMailbox{keywords: true, reclamation: true}
				cx, sx = box.selectedAccountService(t, account, folder, nil, false, false), box.selectedAccountService(t, account, folder, nil, false, false)
				t.Cleanup(func() { cx.Close(); sx.Close() })
			}
			clientLanes = append(clientLanes, transport.RosterLane{Plane: plane, Executor: cx})
			serverLanes = append(serverLanes, transport.RosterLane{Plane: plane, Executor: sx})
		}
	}
	cr, sr := exchangeSelectedRoster(t, f, ch, sh, data, plan, clientLanes, serverLanes)
	defer cr.DataWallet().Close()
	m := cr.Manifest()
	if len(m.Accounts) != 10 || m.ClientBudget.Connections != 20 || m.ServerBudget.Connections != 30 {
		t.Fatal("roster inferred or expanded socket budgets", m)
	}
	for _, a := range m.Accounts {
		if a.FastUIDValidity != 9 || a.BulkUIDValidity != 9 {
			t.Fatal("unverified selected scope", a)
		}
	}
	m.Accounts[0].Account = "mutated observer"
	if cr.Manifest().Accounts[0].Account == m.Accounts[0].Account || sr.Manifest().Accounts[0].Account == m.Accounts[0].Account {
		t.Fatal("mutable diagnostic manifest escaped")
	}
}

func TestRosterDisagreementCannotOpenDataWalletOrSendAccept(t *testing.T) {
	_, c, s, _, _, path := rosterFixtureWithPlans(t, func(p *transport.RosterPlan) { p.ClientBudget = transport.RuntimeBudget{Connections: 3, PerAccount: 3} })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.Run(ctx); done <- err }()
	if _, err := c.Run(ctx); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("different authenticated socket plan accepted", err)
	}
	cancel()
	<-done
	if state := c.Snapshot(); state.Prepared != 0 || state.RosterConfirmed {
		t.Fatal("roster disagreement emitted ACCEPT", state)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unagreed roster created DATA state", err)
	}
}

func TestRosterRejectsInvalidOwnershipBeforeConsumingBootstrapCapability(t *testing.T) {
	f := requestedInvitationFixture(t, nil, nil, false)
	ch, sh := exchangeDrivenBootstrap(t, f)
	box := &sharedMailbox{reclamation: true, keywords: true}
	cx, sx := box.selectedWithIdleService(t, "bulk", nil, false, false), box.selectedWithIdleService(t, "bulk", nil, false, false)
	defer cx.Close()
	defer sx.Close()
	plan := transport.RosterPlan{Accounts: []transport.RosterAccount{{Account: "account1", FastFolder: "fast", BulkFolder: "bulk"}}, ClientBudget: transport.RuntimeBudget{Connections: 2, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 3, PerAccount: 3}}
	lanes := []transport.RosterLane{{Plane: transport.Fast, Executor: f.cx}, {Plane: transport.Bulk, Executor: cx}}
	path := filepath.Join(t.TempDir(), "data-wallet.db")
	before, err := f.wallet.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]transport.RosterLane{nil, {lanes[0], lanes[0]}, {{Plane: transport.Bulk, Executor: f.cx}, lanes[1]}} {
		if _, err := transport.NewClientRoster(ch, plan, bad, f.wallet, path, f.key); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("invalid lane ownership accepted", err)
		}
	}
	if _, err := transport.NewClientRoster(ch, plan, lanes, f.wallet, path, bytes.Repeat([]byte{9}, 32)); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("foreign profile accepted", err)
	}
	badPlan := plan
	badPlan.ClientBudget.Connections = 1
	if _, err := transport.NewClientRoster(ch, badPlan, lanes, f.wallet, path, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("insufficient real socket budget accepted", err)
	}
	if _, err := transport.NewServerRoster(sh, plan, []transport.RosterLane{{Plane: transport.Fast, Executor: f.sx}, {Plane: transport.Bulk, Executor: sx}}, f.store, f.store, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("shared CONTROL/DATA issuer accepted", err)
	}
	client, err := transport.NewClientRoster(ch, plan, lanes, f.wallet, path, f.key)
	if err != nil {
		t.Fatal("failed constructor consumed ownership", err)
	}
	defer client.Close()
	if _, err := transport.NewClientRoster(ch, plan, lanes, f.wallet, path, f.key); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("consumed bootstrap reused", err)
	}
	after, err := f.wallet.Entries()
	if err != nil || len(before) != len(after) {
		t.Fatal(err)
	}
	for n := range before {
		if before[n].Used != after[n].Used || len(before[n].Pending) != len(after[n].Pending) {
			t.Fatal("constructors spent permission")
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("constructor created DATA state before roster", err)
	}
}

func TestRosterRetainsBootstrapUIDKnowledgeAcrossTemporaryReplicaAbsence(t *testing.T) {
	f, c, s, _, _, _ := rosterFailureFixture(t)
	f.box.mu.Lock()
	saved := append([][]byte(nil), f.box.bodies...)
	savedBytes := f.box.bytes
	clear(f.box.bodies)
	f.box.bytes = 0
	f.box.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		r   *transport.RosterResult
		err error
	}
	done := make(chan result, 2)
	go func() { r, err := c.Run(ctx); done <- result{r, err} }()
	deadline := time.After(3 * time.Second)
	for c.Snapshot().DiscoveryTurns < 2 {
		select {
		case <-deadline:
			t.Fatal("client never observed missing replica history")
		case <-time.After(time.Millisecond):
		}
	}
	// Restore actual original UIDs, not replacement bodies or a higher watermark.
	f.box.mu.Lock()
	copy(f.box.bodies, saved)
	f.box.bytes = savedBytes
	f.box.mu.Unlock()
	go func() { r, err := s.Run(ctx); done <- result{r, err} }()
	var failure error
	for range 2 {
		r := <-done
		if r.err != nil {
			failure = errors.Join(failure, r.err)
			cancel()
		} else if r.r.DataWallet() != nil {
			defer r.r.DataWallet().Close()
		}
	}
	if failure != nil {
		t.Fatal("startup history was decoded as a roster frame after temporary absence", failure, c.Snapshot(), s.Snapshot())
	}
	if !c.Snapshot().RosterConfirmed || !s.Snapshot().RosterConfirmed {
		t.Fatal("replica absence hid actual roster messages")
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}
