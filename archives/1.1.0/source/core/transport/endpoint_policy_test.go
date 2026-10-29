package transport

import (
	"context"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

// Policy-only fixture: real bounded journals and production authority queue,
// with no claimed wire delivery or peer authentication. Public endpoint tests
// separately cover authenticated bootstrap, split physical capacity and HTTPS.
func endpointPolicyFixture(t *testing.T) (*EndpointSession, *Session, wire.ID) {
	t.Helper()
	const account, user = "account1", "12345678"
	current := wire.ID{1}
	session, err := NewSession(7, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	stores := make([]*journal.Store, 2)
	for n := range stores {
		cfg := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: account, Provider: "fixture", Limit: pool.Amount{Bytes: 1 << 20, Messages: 128}, Users: []journal.User{{ID: user, Limit: pool.Amount{Bytes: 1 << 20, Messages: 128}, Folders: []string{"fast"}}}}}}
		stores[n], err = journal.Open(filepath.Join(t.TempDir(), "original.db"), cfg, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stores[n].Close() })
		if err = stores[n].EnableAfterInventory(account, user, stores[n].Run()); err != nil {
			t.Fatal(err)
		}
	}
	owner := &AuthorityController{session: session, recovery: true, account: account, user: user, sessionID: current, issues: map[LedgerClass]authorityIssue{}, routes: map[LedgerClass]authorityRoute{}}
	for n, class := range []LedgerClass{ControlLedger, DataLedger} {
		owner.routes[class] = authorityRoute{store: stores[n], id: stores[n].StoreID(), run: stores[n].Run()}
	}
	endpoint := &EndpointSession{Roster: &RosterResult{journals: map[string]RosterJournals{account: {Account: account, Control: stores[0], Data: stores[1]}}}, Service: &RosterService{Authorities: map[string]*AuthorityController{account: owner}}, reconciled: map[wire.QuotaKey]bool{}}
	return endpoint, session, current
}
func policyOldGrant(t *testing.T, store *journal.Store, amount pool.Amount) wire.StorageGrant {
	t.Helper()
	g, err := store.IssueRemote(journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{77}}, amount)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}
func TestFullLedgerStillReoffersHistoricalGrantAfterFreshOtherLedgerPermission(t *testing.T) {
	endpoint, session, current := endpointPolicyFixture(t)
	owner := endpoint.Service.Authorities["account1"]
	store := owner.routes[ControlLedger].store
	old := policyOldGrant(t, store, pool.Amount{Bytes: 1 << 20, Messages: 128})
	before, _ := store.Held("account1", "12345678")
	if err := endpoint.serverPolicyFor(context.Background(), map[string]map[LedgerClass]uint64{}, "12345678", current); err != nil {
		t.Fatal(err)
	}
	if len(session.authorityOut) != 2 {
		t.Fatal("full ledger suppressed its recovery offer", len(session.authorityOut))
	}
	class, fresh, err := decodeAuthority(session.authorityOut[0].Payload)
	if err != nil || class != DataLedger || authorityKey(fresh).Session != current {
		t.Fatal("fresh DATA was starved by history", class, fresh, err)
	}
	class, recovery, err := decodeAuthority(session.authorityOut[1].Payload)
	recovered, ok := recovery.(wire.StorageGrant)
	if err != nil || !ok || class != ControlLedger || recovered != old || !endpoint.reconciled[old.Key] {
		t.Fatal("wrong original recovery offer", class, recovery, err)
	}
	after, _ := store.Held("account1", "12345678")
	if after != before {
		t.Fatal("metadata re-offer changed debt", before, after)
	}
}
func TestPendingGrantRetryPreservesAmountWhenRecoveryChangesAvailableQuota(t *testing.T) {
	endpoint, session, current := endpointPolicyFixture(t)
	owner := endpoint.Service.Authorities["account1"]
	store := owner.routes[DataLedger].store
	old := policyOldGrant(t, store, pool.Amount{Bytes: 1 << 19, Messages: 64})
	amount, err := endpointAllowance(store, "account1", "12345678", current)
	if err != nil {
		t.Fatal(err)
	}
	for range maxAuthorityQueued {
		if err = session.queueAuthorityPriority(DataLedger, old, false); err != nil {
			t.Fatal(err)
		}
	}
	ref, err := owner.Issue(context.Background(), DataLedger, 1, amount)
	if !errors.Is(err, ErrCapacity) || ref.ID == 0 {
		t.Fatal(ref, err)
	}
	if _, err = store.ApplyRemote("12345678", store.Run(), wire.StorageRelease{Key: old.Key}); err != nil {
		t.Fatal(err)
	}
	changed, err := endpointAllowance(store, "account1", "12345678", current)
	if err != nil || changed == amount {
		t.Fatal("fixture did not change headroom", changed, amount, err)
	}
	// Model draining logical records, without changing any financial journal.
	session.mu.Lock()
	for _, r := range session.authorityOut {
		clear(r.Payload)
	}
	session.authorityOut = nil
	session.authorityCount = 0
	session.authorityBytes = 0
	session.buffered = 0
	session.mu.Unlock()
	sequences := map[string]map[LedgerClass]uint64{}
	if err = endpoint.serverPolicyFor(context.Background(), sequences, "12345678", current); err != nil {
		t.Fatal("pending issue lost idempotency after recovery", err)
	}
	saved := owner.issues[DataLedger]
	if !saved.queued || saved.ref != ref || saved.limit != amount || saved.request != 1 || sequences["account1"][DataLedger] != 1 {
		t.Fatal("retry changed original issue", saved, ref, amount, sequences)
	}
	grants, err := store.ScanGrants(0, 256)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, g := range grants {
		if g.Remote && g.Ref.Scope.Session == current {
			count++
			if g.Ref != ref || g.Limit != amount {
				t.Fatal(g)
			}
		}
	}
	if count != 1 {
		t.Fatal("retry allocated extra grants", count)
	}
}
func TestHistoricalOfferBacklogCannotFillQueueBeforeFreshPermissions(t *testing.T) {
	endpoint, session, current := endpointPolicyFixture(t)
	owner := endpoint.Service.Authorities["account1"]
	for range maxAuthorityQueued {
		policyOldGrant(t, owner.routes[ControlLedger].store, pool.Amount{Bytes: 128, Messages: 1})
	}
	if err := endpoint.serverPolicyFor(context.Background(), map[string]map[LedgerClass]uint64{}, "12345678", current); err != nil {
		t.Fatal(err)
	}
	if len(session.authorityOut) != 2+maxHistoricalAuthorityQueued {
		t.Fatal("fixture did not saturate bounded queue", len(session.authorityOut))
	}
	for n, class := range []LedgerClass{ControlLedger, DataLedger} {
		got, message, err := decodeAuthority(session.authorityOut[n].Payload)
		if err != nil || got != class || authorityKey(message).Session != current {
			t.Fatal("history starved initial permission", n, got, message, err)
		}
	}
	if len(endpoint.reconciled) != maxHistoricalAuthorityQueued {
		t.Fatal("unqueued history was marked delivered", len(endpoint.reconciled))
	}
}

func TestAllCurrentGrantsExhaustedBeforePolicyTickStillFenceExactSpend(t *testing.T) {
	s, err := NewSession(7, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dw, err := wallet.Open(filepath.Join(t.TempDir(), "data.db"), "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer dw.Close()
	cw, err := wallet.Open(filepath.Join(t.TempDir(), "control.db"), "12345678", wire.ID{2})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()
	for n := uint64(1); n <= 2; n++ {
		offer := wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: n, Session: dw.Session(), User: "12345678", Account: "account1"}, Limit: wire.Quota{Bytes: 4096, Messages: 1}}
		if err = dw.Accept(offer); err != nil {
			t.Fatal(err)
		}
	}
	owner := &AuthorityController{session: s, client: true, account: "account1", user: "12345678", sessionID: dw.Session(), routes: map[LedgerClass]authorityRoute{ControlLedger: {wallet: cw, id: wire.ID{2}, run: 1}, DataLedger: {wallet: dw, id: wire.ID{1}, run: 1}}}
	ep := &EndpointSession{Service: &RosterService{Authorities: map[string]*AuthorityController{"account1": owner}}, Roster: &RosterResult{wallets: map[string]RosterWallets{"account1": {Control: cw, Data: dw}}}}
	if err = ep.clientPolicy(context.Background()); err != nil || len(s.authorityOut) != 0 {
		t.Fatal("fresh one-message grants were fenced before any publication", err, len(s.authorityOut))
	}
	for range 2 {
		ticket, e := dw.Reserve(4096)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = ticket.Commit("fast", []byte("opaque authenticated fixture"), make([]byte, 32), func(b []byte) (uint64, error) { return uint64(len(b)), nil }); e != nil {
			t.Fatal(e)
		}
	}
	before, err := dw.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if err = ep.clientPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := dw.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.authorityOut) != 2 {
		t.Fatal("all-spent grants deadlocked fresh permission", len(s.authorityOut))
	}
	for n, e := range after {
		if !e.Fenced || e.Used != before[n].Used || e.Used.Messages != 1 {
			t.Fatal("fencing forgot or refunded original spend", before[n], e)
		}
	}
	for _, r := range s.authorityOut {
		class, message, e := decodeAuthority(r.Payload)
		release, ok := message.(wire.StorageRelease)
		if e != nil || !ok || class != DataLedger || release.Final.Messages != 1 || release.Final.Bytes == 0 {
			t.Fatal("fence lost exact lifetime spend", message, e)
		}
	}
	if err = ep.clientPolicy(context.Background()); err != nil || len(s.authorityOut) != 2 {
		t.Fatal("policy duplicated fences", err, len(s.authorityOut))
	}
}

func TestSurvivingWalletFinalSpendRemainsEssentialDuringFirstRequestGrace(t *testing.T) {
	s, err := NewSession(7, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(t.TempDir(), "surviving-data.db")
	dw, err := wallet.Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	offer := wire.StorageGrant{Key: wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: dw.Session(), User: "12345678", Account: "account1"}, Limit: wire.Quota{Bytes: 4096, Messages: 8}}
	if err = dw.Accept(offer); err != nil {
		t.Fatal(err)
	}
	ticket, err := dw.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = ticket.Commit("fast", []byte("original lifetime spend"), make([]byte, 32), func(b []byte) (uint64, error) { return uint64(len(b)), nil }); err != nil {
		t.Fatal(err)
	}
	before, _ := dw.Entries()
	dw.Close()
	dw, err = wallet.Open(path, "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer dw.Close()
	cw, err := wallet.Open(filepath.Join(t.TempDir(), "control.db"), "12345678", wire.ID{2})
	if err != nil {
		t.Fatal(err)
	}
	defer cw.Close()
	owner := &AuthorityController{session: s, client: true, recovery: true, account: "account1", user: "12345678", sessionID: dw.Session(), routes: map[LedgerClass]authorityRoute{ControlLedger: {wallet: cw, id: wire.ID{2}, run: 1}, DataLedger: {wallet: dw, id: wire.ID{1}, run: 1}}}
	runtime := &Runtime{session: s}
	ep := &EndpointSession{Service: &RosterService{Runtime: runtime, Authorities: map[string]*AuthorityController{"account1": owner}}, Roster: &RosterResult{wallets: map[string]RosterWallets{"account1": {Control: cw, Data: dw}}}}
	s.lastForeground = time.Now()
	if ep.Service.historicalLaunchAllowed() {
		t.Fatal("speculative historical offers are not deferred")
	}
	for range 2 {
		if err = ep.clientPolicy(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.authorityOut) != 1 {
		t.Fatal("exact surviving final spend was delayed or duplicated", len(s.authorityOut))
	}
	class, message, err := decodeAuthority(s.authorityOut[0].Payload)
	release, ok := message.(wire.StorageRelease)
	if err != nil || !ok || class != DataLedger || release.Key != offer.Key || release.Final != before[0].Used {
		t.Fatal("original final spend changed", message, err)
	}
	after, err := dw.Entries()
	if err != nil || len(after) != 1 || !after[0].Fenced || after[0].Used != before[0].Used || len(after[0].Pending) != len(before[0].Pending) {
		t.Fatal("scheduling released original BODY debt", after, err)
	}
	if err = dw.Acknowledge(wire.CleanupStatus{Key: offer.Key, Used: before[0].Used, Registered: before[0].Used, Closed: true}); err != nil {
		t.Fatal(err)
	}
	s2, err := NewSession(7, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.lastForeground = time.Now()
	owner.session = s2
	owner.fences = nil
	ep.Service.Runtime = &Runtime{session: s2}
	if err = ep.clientPolicy(context.Background()); err != nil || len(s2.authorityOut) != 0 {
		t.Fatal("already acknowledged final spend was re-fenced", err, len(s2.authorityOut))
	}
	after, _ = dw.Entries()
	if !after[0].IssuerClosed || after[0].Retired || after[0].Used != before[0].Used {
		t.Fatal("final spend ack became physical cleanup proof", after)
	}
}
