package transport

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestRetiredAggregateSnapshotsDoNotInventCleanupOrDiscardUnknownPhysicalDebt(t *testing.T) {
	cfg := journal.Config{MaxGrants: 16, MaxAttempts: 16, Accounts: []journal.Account{{ID: "account1", Provider: "fixture", Limit: pool.Amount{Bytes: 1 << 20, Messages: 64}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 1 << 20, Messages: 64}, Folders: []string{"fast"}}}}}}
	s, err := journal.Open(filepath.Join(t.TempDir(), "original.db"), cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnableAfterInventory("account1", "12345678", s.Run()); err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{2}}
	g, err := s.IssueRemote(scope, pool.Amount{Bytes: 16384, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyRemote(scope.User, s.Run(), wire.StorageRelease{Key: offer.Key}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetireRemote(scope.User, s.Run(), wire.Retirement{Key: offer.Key}); err != nil {
		t.Fatal(err)
	}
	scope.Session = wire.ID{1}
	local, err := s.Issue(scope, pool.Amount{Bytes: 32768, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.PrepareStarted(local.Ref, "fast", wire.ID{7}, 128, []byte("original unknown APPEND"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(attempt.Ref, journal.Uncertain); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Held(scope.Account, scope.User)
	a := &AuthorityController{recovery: true, sessionID: scope.Session, account: scope.Account, user: scope.User, routes: map[LedgerClass]authorityRoute{DataLedger: {store: s, id: s.StoreID(), run: s.Run()}}}
	// No Session/queue is supplied: a fabricated cleanup acknowledgement or
	// grant would reach that missing owner rather than remaining a pure no-op.
	for _, msg := range []wire.Control{wire.UsageReport{Key: offer.Key}, wire.StorageRelease{Key: offer.Key}} {
		payload, err := encodeAuthority(DataLedger, msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.apply(payload); err != nil {
			t.Fatal("late retired aggregate killed the current session", err)
		}
	}
	unknown := offer.Key
	descriptor := wire.UsageReport{Key: offer.Key, Total: wire.Quota{Bytes: 128, Messages: 1}, Attempt: &wire.PhysicalDescriptor{Batch: wire.ID{8}, EncodedBytes: 128, DecodedBytes: 12, Digest: [32]byte{1}, Folder: "fast"}}
	encoded, err := encodeAuthority(DataLedger, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.apply(encoded); !errors.Is(err, journal.ErrMissing) {
		t.Fatal("missing physical descriptor was treated as an obsolete aggregate", err)
	}
	unknown.Grant += 1000
	payload, err := encodeAuthority(DataLedger, wire.UsageReport{Key: unknown})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.apply(payload); !errors.Is(err, journal.ErrScope) {
		t.Fatal("unallocated future grant was silently accepted", err)
	}
	after, _ := s.Held(scope.Account, scope.User)
	saved, err := s.GetAttempt(attempt.Ref)
	if err != nil || saved.State != journal.Unknown || after != before {
		t.Fatal("obsolete metadata refunded or forgot unknown physical debt", saved.State, before, after, err)
	}
	if err := s.Audit(); err != nil {
		t.Fatal(err)
	}
}
