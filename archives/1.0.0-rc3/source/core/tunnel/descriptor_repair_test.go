package tunnel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestDescriptorGapRequestsDurableReplayWithoutRefund(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{2}}, pool.Amount{Bytes: 10000, Messages: 10})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, urgentQueue: make(chan queuedControl, 8)}
	reports := make([]wire.UsageReport, 3)
	for i := range reports {
		reports[i] = wire.UsageReport{Key: offer.Key, Total: wire.Quota{Bytes: uint64(i+1) * 1000, Messages: uint64(i + 1)}, Attempt: &wire.PhysicalDescriptor{Batch: wire.ID{byte(i + 1)}, EncodedBytes: 1000, DecodedBytes: 100, Digest: [32]byte{byte(i + 1)}, Folder: u.Up}}
	}
	if _, err := s.ApplyRemote(u.ID, s.Run(), reports[0]); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Held(a.ID, u.ID)
	if _, err := s.ApplyRemote(u.ID, s.Run(), reports[2]); !errors.Is(err, journal.ErrOrder) {
		t.Fatal("journal accepted noncontiguous descriptor", err)
	}
	for i := 0; i < 10; i++ {
		ok, err := l.repairDescriptorGap(reports[2])
		if !ok || err != nil {
			t.Fatal(ok, err)
		}
	}
	if len(l.urgentQueue) != 1 {
		t.Fatal("gap amplified repair traffic", len(l.urgentQueue))
	}
	c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
	if err != nil {
		t.Fatal(err)
	}
	if request, ok := c.(wire.DescriptorRequest); !ok || request.Key != offer.Key || request.Next != 2 {
		t.Fatal("wrong repair prefix", c)
	}
	after, _ := s.Held(a.ID, u.ID)
	status, _ := s.RemoteStatus(g.Ref)
	if after != before || status.Registered != reports[0].Total || status.Released != (wire.Quota{}) {
		t.Fatal("gap mutated/refunded accounting", before, after, status)
	}
	for _, r := range reports[1:] {
		if _, err := s.ApplyRemote(u.ID, s.Run(), r); err != nil {
			t.Fatal(err)
		}
	}
	bad := reports[2]
	bad.Total = wire.Quota{Bytes: 4000, Messages: 4}
	// Adjacent ordinals with inconsistent encoded length are still fatal.
	bad.Total.Bytes++
	if ok, err := l.repairDescriptorGap(bad); ok || err != nil {
		t.Fatal("malformed descriptor reclassified", ok, err)
	}
	bad = reports[2]
	bad.Key.Account = "a02"
	if ok, err := l.repairDescriptorGap(bad); ok || err != nil {
		t.Fatal("cross-account repair", ok, err)
	}
	if err := s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestDescriptorReplayUsesWalletAndBootstrapDefersOldRelease(t *testing.T) {
	w, err := wallet.Open(filepath.Join(t.TempDir(), "wallet.db"), "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}
	if err := w.Accept(wire.StorageGrant{Key: k, Limit: wire.Quota{Bytes: 10000, Messages: 10}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := w.Spend(k, "up", []byte{byte(i)}, func([]byte) (uint64, error) { return 1000, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.Fence(k); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), wallet: w, account: servercfg.Account{ID: "a01"}, user: servercfg.User{ID: "12345678"}, urgentQueue: make(chan queuedControl, 8), controlQueue: make(chan queuedControl, 8), maintenanceQueue: make(chan queuedControl, 8)}
	before, _ := w.Entries()
	if err := l.maintenance(); err != nil {
		t.Fatal(err)
	}
	if len(l.urgentQueue)+len(l.controlQueue)+len(l.maintenanceQueue) != 0 {
		t.Fatal("wallet reconciliation overtook bootstrap confirmation")
	}
	// Binding the wallet enables reconciliation even before any fresh grant:
	// requiring one would deadlock when the old reservations fill the journal.
	serverBoot, err := session.NewBootstrap(session.Server, 1, 0, wire.ID{1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	clientBoot, err := session.NewBootstrap(session.Client, 1, 0, wire.ID{1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	offer, err := serverBoot.Poll(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientBoot.Receive(1, 0, *offer.Send); err != nil {
		t.Fatal(err)
	}
	if _, err := clientBoot.CommitStore(1, 0, wire.ID{1}); err != nil {
		t.Fatal(err)
	}
	l.boot = clientBoot
	if err := l.maintenance(); err != nil {
		t.Fatal(err)
	}
	if l.ready.Load() || len(l.urgentQueue) != 3 {
		t.Fatal("reconciliation requires a fresh grant")
	}
	for len(l.urgentQueue) > 0 {
		<-l.urgentQueue
	}
	if err := l.replayDescriptors(wire.DescriptorRequest{Key: k, Next: 2}); err != nil {
		t.Fatal(err)
	}
	if len(l.urgentQueue) != 2 {
		t.Fatal("wrong replay size", len(l.urgentQueue))
	}
	for ordinal := uint64(2); ordinal <= 3; ordinal++ {
		c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
		if err != nil {
			t.Fatal(err)
		}
		r, ok := c.(wire.UsageReport)
		if !ok || r.Total.Messages != ordinal || r.Attempt == nil {
			t.Fatal("wallet descriptor not retained", c)
		}
	}
	after, _ := w.Entries()
	if after[0].Used != before[0].Used || len(after[0].Pending) != 3 {
		t.Fatal("control replay spent/refunded DATA quota")
	}
	bad := k
	bad.Account = "a02"
	if err := l.replayDescriptors(wire.DescriptorRequest{Key: bad, Next: 1}); !errors.Is(err, journal.ErrScope) {
		t.Fatal("cross-account replay", err)
	}
}

func TestBootstrapToleratesReorderedWalletReconciliation(t *testing.T) {
	l := &lane{side: true, account: servercfg.Account{ID: "a01"}, user: servercfg.User{ID: "12345678"}}
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: wire.ID{2}, User: l.user.ID, Account: l.account.ID}
	for _, c := range []wire.Control{wire.StorageRelease{Key: k, Final: wire.Quota{Bytes: 1000, Messages: 1}}, wire.UsageReport{Key: k, Total: wire.Quota{Bytes: 1000, Messages: 1}}, wire.Retirement{Key: k, Final: wire.Quota{Bytes: 1000, Messages: 1}}} {
		f, err := wire.EncodeControl(0, c)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := l.deferBootstrapReconciliation(f)
		if !ok || err != nil {
			t.Fatal("reordered reconciliation killed bootstrap", ok, err)
		}
	}
	k.Account = "a02"
	f, _ := wire.EncodeControl(0, wire.StorageRelease{Key: k, Final: wire.Quota{Bytes: 1000, Messages: 1}})
	if ok, err := l.deferBootstrapReconciliation(f); ok || !errors.Is(err, journal.ErrScope) {
		t.Fatal("cross-account pre-bootstrap reconciliation", ok, err)
	}
	f, _ = wire.EncodeControl(0, wire.Echo{Token: 1})
	if ok, err := l.deferBootstrapReconciliation(f); ok || err != nil {
		t.Fatal("unrelated traffic bypassed bootstrap", ok, err)
	}
}
