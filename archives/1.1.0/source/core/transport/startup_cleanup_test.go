package transport

import (
	"bytes"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wire"
	"strconv"
	"testing"
)

func TestStartupAllowanceRequiresRealCapacityForFourClientAndSixServerMessages(t *testing.T) {
	for _, free := range []uint64{128, 16, 15, 11, 10, 9} {
		t.Run(strconv.FormatUint(free, 10), func(t *testing.T) {
			ep, _, _ := endpointPolicyFixture(t)
			store := ep.Service.Authorities["account1"].routes[ControlLedger].store
			policyOldGrant(t, store, pool.Amount{Bytes: 4096, Messages: 128 - free})
			before, _ := store.Held("account1", "12345678")
			allowance, err := endpointInitialAllowance(store, "account1", "12345678")
			if free >= 10 && (err != nil || allowance.Messages != 4) {
				t.Fatal("usable original startup capacity rejected", free, allowance, err)
			}
			if free < 10 && !errors.Is(err, journal.ErrLimit) {
				t.Fatal("insufficient real capacity admitted", free, allowance, err)
			}
			after, _ := store.Held("account1", "12345678")
			if after != before {
				t.Fatal("allowance calculation allocated or forgave quota", before, after)
			}
		})
	}
}

func TestStartupCleanupCandidatesRemainScopedAndNeverSupplyAbsenceProof(t *testing.T) {
	ep, s, sid := endpointPolicyFixture(t)
	store := ep.Service.Authorities["account1"].routes[ControlLedger].store
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: sid, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	key := bytes.Repeat([]byte{8}, 32)
	client, err := secure.NewSessionHandshake(binding, key, true)
	if err != nil {
		t.Fatal(err)
	}
	server, err := secure.NewSessionHandshake(binding, key, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Read(first); err != nil {
		t.Fatal(err)
	}
	second, err := server.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = client.Read(second); err != nil {
		t.Fatal(err)
	}
	epoch, err := server.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: "account1", User: "12345678", Session: sid}
	grant, err := store.Issue(scope, pool.Amount{Bytes: 4096, Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("original observed startup BODY")
	attempt, err := store.PrepareStarted(grant.Ref, "fast", wire.ID{7}, 128, body)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Complete(attempt.Ref, journal.Accepted); err != nil {
		t.Fatal(err)
	}
	location := journal.Location{UIDValidity: 9, UID: 1}
	if err = store.Observe(attempt.Ref, store.Run(), journal.Observation{Account: scope.Account, Folder: "fast", EncodedBytes: 128, Location: location, Decoded: body}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Held(scope.Account, scope.User)
	candidate := &reclaimBody{startup: true, store: store, ref: attempt.Ref, location: location, account: scope.Account, folder: "fast", receiptKeyword: receiptKeyword(body, 9, 1, key)}
	handoff := &BootstrapHandoff{epoch: epoch, origin: ServerOrigin{Store: store.StoreID(), Run: store.Run(), Account: scope.Account, User: scope.User, Folder: "fast", UIDValidity: 9}, cleanup: startupCleanup{reclaim: map[uint32]*reclaimBody{1: candidate}}}
	inbox := &Inbox{session: s, server: true, executor: &Executor{uidPlus: true}, scope: InboxScope{Account: scope.Account, Folder: "fast", UIDValidity: 9}, limits: DefaultInboxLimits(), bootstrapUIDs: map[uint32]bool{1: true}}
	for _, mutate := range []func(*reclaimBody){
		func(r *reclaimBody) { r.account = "account2" }, func(r *reclaimBody) { r.folder = "bulk" }, func(r *reclaimBody) { r.location.UIDValidity++ }, func(r *reclaimBody) { r.location.UID++ }, func(r *reclaimBody) { r.ref.Grant.Scope.Session = wire.ID{88} }, func(r *reclaimBody) { r.ref.ID = 0 }, func(r *reclaimBody) { r.startup = false },
	} {
		changed := *candidate
		mutate(&changed)
		handoff.cleanup.reclaim[1] = &changed
		if err = inbox.adoptStartupCleanup(handoff); !errors.Is(err, ErrProtocol) {
			t.Fatal("inconsistent startup candidate adopted", err)
		}
	}
	handoff.cleanup.reclaim[1] = candidate
	if err = inbox.adoptStartupCleanup(handoff); err != nil {
		t.Fatal(err)
	}
	candidate.folder = "changed caller copy"
	if err = inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if !inbox.startupCleanupPending() || inbox.reclaimQueue[1].folder != "fast" {
		t.Fatal("original candidate not independently retained")
	}
	after, _ := store.Held(scope.Account, scope.User)
	saved, err := store.GetAttempt(attempt.Ref)
	if err != nil || after != before || saved.State != journal.Committed {
		t.Fatal("handoff or queue forged release", before, after, saved, err)
	}
	if err = store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestLightControlReservationCannotLimitBulkOrDataCapacity(t *testing.T) {
	_, s, _ := endpointPolicyFixture(t)
	original := pool.Amount{Bytes: 1 << 20, Messages: 32}
	light := s.controlAllowance(original)
	if light.Bytes != original.Bytes || light.Messages != 8 {
		t.Fatal("light CONTROL lease incorrectly sized", light)
	}
	s.buffered = uint64(s.limits.InteractiveBytes) + 1
	if s.controlAllowance(original) != original {
		t.Fatal("bulk sender lost original CONTROL capacity")
	}
	s.buffered = 0
	s.received = uint64(s.limits.InteractiveBytes) + 1
	if s.controlAllowance(original) != original {
		t.Fatal("bulk receiver lost original CONTROL capacity")
	}
}
