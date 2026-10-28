package transport

import (
	"context"
	"testing"
	"time"

	"itom.local/core/pool"
)

func TestShortRequestDefersHistoricalLaunchesWithoutDelayingFreshPermissionOrForgivingDebt(t *testing.T) {
	endpoint, session, current := endpointPolicyFixture(t)
	endpoint.Service.Runtime = &Runtime{session: session}
	oldStore := endpoint.Service.Authorities["account1"].routes[ControlLedger].store
	old := policyOldGrant(t, oldStore, pool.Amount{Bytes: 4096, Messages: 1})
	before, err := oldStore.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	session.lastForeground = time.Now()
	sequences := map[string]map[LedgerClass]uint64{}
	if err = endpoint.serverPolicyFor(context.Background(), sequences, "12345678", current); err != nil {
		t.Fatal(err)
	}
	if endpoint.reconciled[old.Key] || len(session.authorityOut) != 2 {
		t.Fatal("history ran ahead of the first request or fresh permission was suppressed", len(session.authorityOut))
	}
	for _, r := range session.authorityOut {
		_, message, err := decodeAuthority(r.Payload)
		if err != nil || authorityKey(message).Session != current {
			t.Fatal("non-current work consumed the foreground grace", err)
		}
	}
	freshHeld, err := oldStore.Held("account1", "12345678")
	if err != nil || freshHeld.Bytes < before.Bytes || freshHeld.Messages < before.Messages {
		t.Fatal("fresh issuance lost original debt", before, freshHeld, err)
	}
	// An idle session resumes the SAME original offer after the unchanged OPEN
	// lifetime. This does not expire or release the old allocation.
	session.lastForeground = time.Now().Add(-openTimeout)
	if err = endpoint.serverPolicyFor(context.Background(), sequences, "12345678", current); err != nil {
		t.Fatal(err)
	}
	if !endpoint.reconciled[old.Key] {
		t.Fatal("quiet recovery was stranded")
	}
	after, err := oldStore.Held("account1", "12345678")
	// Merely re-offering history changes no original allocation.
	if err != nil || after != freshHeld {
		t.Fatal("scheduling forgave original debt", before, after, err)
	}
	held := after
	session.lastForeground = time.Now()
	session.buffered = uint64(session.limits.InteractiveBytes) + 1
	if !endpoint.Service.historicalLaunchAllowed() {
		t.Fatal("bulk demand stranded capacity recovery")
	}
	if err = endpoint.serverPolicyFor(context.Background(), sequences, "12345678", current); err != nil {
		t.Fatal(err)
	}
	after, err = oldStore.Held("account1", "12345678")
	if err != nil || after != held {
		t.Fatal("repeated policy minted or refunded capacity", held, after, err)
	}
}

func TestRuntimeMaintenanceYieldsButCannotStrandBoundedCleanup(t *testing.T) {
	ep, s, _ := endpointPolicyFixture(t)
	writer := &PublicationWorker{session: s}
	runtime := &Runtime{session: s, carriers: []RuntimeCarrier{{Writer: writer}}}
	ep.Service.Runtime = runtime
	s.runtime = runtime
	s.lastForeground = time.Now()
	inbox := &Inbox{session: s, receipts: map[uint32]*deliveryReceipt{}, reclaimQueue: map[uint32]*reclaimBody{}}
	worker := &InboxWorker{inbox: inbox}
	store := ep.Service.Authorities["account1"].routes[ControlLedger].store
	policyOldGrant(t, store, pool.Amount{Bytes: 4096, Messages: 1})
	before, err := store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	if worker.maintenanceAllowed() {
		t.Fatal("optional cleanup competes with short foreground work")
	}
	writer.stats.ControlBlocked = true
	if !worker.maintenanceAllowed() {
		t.Fatal("blocked current permission could not progress original cleanup")
	}
	writer.stats.ControlBlocked = false
	for uid := uint32(1); uid <= 2*maxAuthorityBatchRecords; uid++ {
		inbox.receipts[uid] = &deliveryReceipt{}
	}
	if !worker.maintenanceAllowed() {
		t.Fatal("bounded receipt queue could strand peer cleanup")
	}
	clear(inbox.receipts)
	s.lastForeground = time.Now().Add(-openTimeout)
	if !worker.maintenanceAllowed() {
		t.Fatal("idle maintenance failed to resume")
	}
	after, err := store.Held("account1", "12345678")
	if err != nil || before != after {
		t.Fatal("scheduling fabricated cleanup proof", before, after, err)
	}
}
