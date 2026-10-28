package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestHistoricalReceivePressureReservesExistingControlCapacityForLiveOpen(t *testing.T) {
	ep, s, _ := endpointPolicyFixture(t)
	s.authorities = ep.Service.Authorities
	x, _ := standbyPeer(t)
	x.mailbox, x.validity = "fast", 7
	i, e := newScopedInbox(x, s, InboxScope{"a02", "fast", 7}, DefaultInboxLimits(), func(uint32, Body, inboxAdmission) (InboxDelivery, error) { return nil, nil })
	if e != nil {
		t.Fatal(e)
	}
	i.pending[1] = &ledgerDelivery{InboxDelivery: InboxAction(func() error { return ErrCapacity }), class: ControlLedger, background: true}
	available := i.admission()
	if !available.control || available.recovery {
		t.Fatal("recovery consumed the reserved live slot", available)
	}
	old := policyOldGrant(t, ep.Service.Authorities["account1"].routes[ControlLedger].store, pool.Amount{Bytes: 4096, Messages: 1})
	payload, e := encodeAuthority(ControlLedger, old)
	if e != nil {
		t.Fatal(e)
	}
	admit, _, e := peerInboxAdmission(s, ControlLedger, available)
	if e != nil {
		t.Fatal(e)
	}
	err := admit([]Record{{Kind: Authority, Generation: 7, Offset: 1, Payload: payload}})
	var blocked *inboxDeferred
	if !errors.As(err, &blocked) || !blocked.background {
		t.Fatal("historical body ignored recovery backpressure", err)
	}
	if e = admit([]Record{{Kind: Open, Generation: 7, Stream: 1}}); e != nil {
		t.Fatal("history blocked independent OPEN", e)
	}
	status, e := encodeAuthority(ControlLedger, wire.CleanupStatus{Key: old.Key})
	if e != nil {
		t.Fatal(e)
	}
	if e = admit([]Record{{Kind: Authority, Generation: 7, Offset: 2, Payload: status}}); e != nil {
		t.Fatal("existing-wallet progress lost liveness", e)
	}
	i.pending[2] = &ledgerDelivery{InboxDelivery: InboxAction(func() error { return ErrCapacity }), class: ControlLedger}
	if i.admission().control || i.admission().recovery {
		t.Fatal("reserved live capacity increased the original memory bound")
	}
}
func TestRosterRejectsOldAuthorityBatchCapabilityBeforeRuntime(t *testing.T) {
	plan, hash, e := canonicalRosterPlan(RosterPlan{Accounts: []RosterAccount{{Account: "account1", FastFolder: "fast", BulkFolder: "bulk"}}, ClientBudget: RuntimeBudget{Connections: 2, PerAccount: 2}, ServerBudget: RuntimeBudget{Connections: 2, PerAccount: 2}})
	if e != nil {
		t.Fatal(e)
	}
	m := RosterManifest{ControlStore: wire.ID{1}, DataStore: wire.ID{2}, ControlRun: 1, DataRun: 1, ClientBudget: plan.ClientBudget, ServerBudget: plan.ServerBudget, Accounts: []RosterAccountState{{RosterAccount: plan.Accounts[0], FastUIDValidity: 7, BulkUIDValidity: 8}}}
	raw, e := encodeRoster(m, plan, hash)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = decodeRoster(raw, plan, hash); e != nil {
		t.Fatal(e)
	}
	raw[4] = 2
	if _, e = decodeRoster(raw, plan, hash); !errors.Is(e, ErrRosterRevision) {
		t.Fatal("old peer could enter unsupported batch runtime", e)
	}
}
func TestFastReadQuantumServicesTheTailWithoutStarvingEarlierUIDs(t *testing.T) {
	bodies := map[uint32][]byte{}
	for n := uint32(1); n <= 5; n++ {
		bodies[n] = []byte("x")
	}
	x := retainedPeer(t, bodies)
	s, e := NewSession(7, DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var visited []uint32
	i, e := newScopedInbox(x, s, InboxScope{"a02", "fast", 7}, DefaultInboxLimits(), func(_ uint32, b Body, _ inboxAdmission) (InboxDelivery, error) {
		visited = append(visited, b.UID)
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	i.readQuantum = 2
	i.pending[99] = &ledgerDelivery{InboxDelivery: InboxAction(func() error { return ErrCapacity }), class: ControlLedger, background: true}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p, e := i.Sweep(ctx)
	if e != nil || !p.More || len(visited) != 2 || visited[0] != 5 || visited[1] != 1 {
		t.Fatal("unbounded FAST turn or stale prefix priority", p, visited, e)
	}
	for len(visited) < 5 {
		if _, e = i.Sweep(ctx); e != nil {
			t.Fatal(e)
		}
	}
	seen := map[uint32]bool{}
	for _, uid := range visited {
		if seen[uid] {
			t.Fatal("bounded turns repeated an observed UID", visited)
		}
		seen[uid] = true
	}
	if len(seen) != 5 {
		t.Fatal("tail preference starved older UIDs", visited)
	}
}
