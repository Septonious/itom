package transport

import (
	"context"
	"testing"
	"time"

	"itom.local/core/journal"
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
	// Bulk demand intentionally grows a new CONTROL lease from the light
	// eight-message cap. That allocation must be current-session authority;
	// the historical offer and original held debt cannot be forgiven.
	if err != nil || after.Bytes < held.Bytes || after.Messages < held.Messages {
		t.Fatal("bulk policy forgave original debt", held, after, err)
	}
	grants, err := oldStore.ScanGrants(0, 256)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, grant := range grants {
		if grant.Ref.ID == old.Key.Grant {
			found = true
			if grant.Closed || grant.Limit.Bytes != old.Limit.Bytes || grant.Limit.Messages != old.Limit.Messages || grant.Used.Messages != 0 || grant.Released.Messages != 0 {
				t.Fatal("scheduling changed the historical allocation", grant)
			}
		} else if grant.Ref.Scope.Session != current {
			t.Fatal("bulk expansion minted historical authority", grant)
		}
	}
	if !found {
		t.Fatal("original historical debt disappeared")
	}
}

func TestRuntimeMaintenanceYieldsButCannotStrandBoundedCleanup(t *testing.T) {
	ep, s, _ := endpointPolicyFixture(t)
	writer := &PublicationWorker{session: s, executor: &Executor{account: "account1"}, sources: []publicationSource{{class: ControlLedger}, {class: DataLedger}}}
	runtime := &Runtime{session: s, carriers: []RuntimeCarrier{{Writer: writer}}}
	ep.Service.Runtime = runtime
	s.runtime = runtime
	s.lastForeground = time.Now()
	inbox := &Inbox{session: s, scope: InboxScope{Account: "account1"}, receipts: map[uint32]*deliveryReceipt{}, reclaimQueue: map[uint32]*reclaimBody{}}
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
	inbox.receiptEnabled = true
	inbox.receipts[1] = &deliveryReceipt{}
	if !worker.maintenanceAllowed() {
		t.Fatal("peer quota could be stranded behind a delayed authenticated receipt")
	}
	clear(inbox.receipts)
	inbox.receiptEnabled = false
	writer.stats.ControlBlocked = true
	if !ep.Service.historicalLaunchAllowed() {
		t.Fatal("quota pressure could not recover surviving historical permissions")
	}
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
	s.authorities = ep.Service.Authorities
	owner := ep.Service.Authorities["account1"]
	owner.issues[DataLedger] = authorityIssue{err: journal.ErrLimit}
	if !worker.maintenanceAllowed() || !ep.Service.historicalLaunchAllowed() {
		t.Fatal("blocked peer grant issuance could not reclaim original capacity")
	}
	delete(owner.issues, DataLedger)
	s.lastForeground = time.Now().Add(-openTimeout)
	if !worker.maintenanceAllowed() {
		t.Fatal("idle maintenance failed to resume")
	}
	after, err := store.Held("account1", "12345678")
	if err != nil || before != after {
		t.Fatal("scheduling fabricated cleanup proof", before, after, err)
	}
}

func TestOneQuotaBlockedAccountDoesNotInterruptHealthyForegroundAccounts(t *testing.T) {
	_, s, _ := endpointPolicyFixture(t)
	s.lastForeground = time.Now()
	first := &PublicationWorker{session: s, executor: &Executor{account: "account1"}, sources: []publicationSource{{class: ControlLedger}, {class: DataLedger}}}
	second := &PublicationWorker{session: s, executor: &Executor{account: "account2"}, sources: []publicationSource{{class: ControlLedger}, {class: DataLedger}}}
	r := &Runtime{session: s, carriers: []RuntimeCarrier{{Writer: first}, {Writer: second}}}
	first.stats.ControlBlocked = true
	if r.permissionPressure() || !r.accountPermissionPressure("account1") || r.accountPermissionPressure("account2") {
		t.Fatal("one blocked account interrupted healthy accounts or lost local cleanup")
	}
	second.stats.ControlBlocked = true
	if !r.permissionPressure() {
		t.Fatal("all accounts blocked but global capacity recovery suppressed")
	}
	second.stats.ControlBlocked = false
	first.stats.ControlBlocked = false
	first.stats.DataBlocked = true
	r.carriers = append(r.carriers, RuntimeCarrier{Writer: &PublicationWorker{session: s, executor: &Executor{account: "account1"}, sources: []publicationSource{{class: DataLedger}}}})
	if r.permissionPressure() || !r.accountPermissionPressure("account1") {
		t.Fatal("shared DATA quota pressure aggregated incorrectly")
	}
	second.stats.DataBlocked = true
	if !r.permissionPressure() {
		t.Fatal("exhausted DATA accounts cannot recover capacity")
	}
}
