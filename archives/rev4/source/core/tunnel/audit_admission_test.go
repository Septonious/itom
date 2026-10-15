package tunnel

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/session"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestAuditedAdmissionAndDurablePressure(t *testing.T) {
	t.Run("quota_limited_server_is_not_ready", func(t *testing.T) {
		a := servercfg.Account{ID: "a01"}
		a.Mail.Host = "mail.example"
		u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
		s, err := ledger(t.TempDir(), "data", a, u, false)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
			t.Fatal(err)
		}
		l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 16)}
		establishRecoveredServer(t, l)
		limit, _ := s.Limit(a.ID, u.ID)
		debt, err := s.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: l.cipher.Context().Session}, pool.Amount{Bytes: limit.Bytes, Messages: limit.Messages})
		if err != nil {
			t.Fatal(err)
		}
		if err := l.offer(); err != nil {
			t.Fatal(err)
		}
		if l.ready.Load() || l.everReady.Load() {
			t.Fatal("quota-limited server advertised ready")
		}
		if err := s.CloseGrant(debt.Ref); err != nil {
			t.Fatal(err)
		}
		if err := l.offer(); err != nil {
			t.Fatal(err)
		}
		if !l.ready.Load() || !l.everReady.Load() {
			t.Fatal("both published classes did not activate server")
		}
	})
	t.Run("wallet_replays_queue_pressure_without_refund", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cipher := cleanupFixtureEpoch(t)
		w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), wire.ID{1}, cipher)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: w.Session(), User: "12345678", Account: "a01"}
		if err := w.Accept(wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: fastLeaseBytes, Messages: fastLeaseMessages}}); err != nil {
			t.Fatal(err)
		}
		report, _, err := w.Spend(key, "up", []byte("durable physical body"), func(b []byte) (uint64, error) { return uint64(len(b) + 100), nil })
		if err != nil {
			t.Fatal(err)
		}
		l := &lane{ctx: ctx, wallet: w, urgentQueue: make(chan queuedControl, 1), maintenanceQueue: make(chan queuedControl, 1)}
		l.boot, err = session.NewBootstrap(session.Client, 1, 0, wire.ID{1}, 0)
		if err != nil {
			t.Fatal(err)
		}
		serverBoot, err := session.NewBootstrap(session.Server, 1, 0, wire.ID{1}, 1)
		if err != nil {
			t.Fatal(err)
		}
		offer, err := serverBoot.Poll(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.boot.Receive(1, 0, *offer.Send); err != nil {
			t.Fatal(err)
		}
		if _, err := l.boot.CommitStore(1, 0, wire.ID{1}); err != nil {
			t.Fatal(err)
		}
		l.urgentQueue <- queuedControl{}
		l.maintenanceQueue <- queuedControl{}
		if err := l.publishRemoteUsage(key, report); err != nil {
			t.Fatal("durable queue pressure killed owner", err)
		}
		entries, err := w.Entries()
		if err != nil || len(entries) != 1 || !entries[0].Fenced || entries[0].Used != report.Total || len(entries[0].Pending) != 1 {
			t.Fatal("durable charge or replay lost", entries, err)
		}
		<-l.urgentQueue
		<-l.maintenanceQueue
		if err := l.maintenance(); err != nil {
			t.Fatal(err)
		}
		if len(l.urgentQueue) != 1 || len(l.maintenanceQueue) != 1 {
			t.Fatal("durable replay queues missing")
		}
		c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
		if err != nil || c.(wire.UsageReport).Total != report.Total {
			t.Fatal("exact pending descriptor not replayed", c, err)
		}
		c, err = wire.DecodeControl((<-l.maintenanceQueue).Frame)
		if err != nil || c.(wire.StorageRelease).Final != report.Total {
			t.Fatal("exact final fence not replayed", c, err)
		}
	})
	checkRekeyKeepsHalfOfAccountsAndWaitsForReplacement(t)
}

func checkRekeyKeepsHalfOfAccountsAndWaitsForReplacement(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	var lanes []*lane
	for _, id := range []string{"a01", "a02", "a03", "a04"} {
		l := stripeTestLane(g, id, true)
		defer l.cancel()
		g.attach(l)
		g.logicalOwners[id] = l
		lanes = append(lanes, l)
	}
	if !g.beginRekey(lanes[0]) || g.beginRekey(lanes[1]) {
		t.Fatal("planned rotations not serialized")
	}
	g.detach(lanes[0])
	if g.beginRekey(lanes[1]) {
		t.Fatal("detach released rotation before replacement readiness")
	}
	fresh := stripeTestLane(g, "a01", true)
	defer fresh.cancel()
	fresh.carrierEpoch = 2
	g.attach(fresh)
	g.completeRekey(fresh)
	if g.beginRekey(lanes[1]) {
		t.Fatal("replacement bypassed fresh echo readiness")
	}
	fresh.rejoinPending.Store(false)
	g.completeRekey(fresh)
	if !g.beginRekey(lanes[1]) {
		t.Fatal("ready replacement did not release rotation slot")
	}
	g.completeRekey(lanes[1])
	lanes[2].ready.Store(false)
	lanes[3].ready.Store(false)
	if g.beginRekey(lanes[1]) {
		t.Fatal("planned rotation took more than half the accounts down")
	}
	lanes[2].ready.Store(true)
	lanes[3].ready.Store(true)
	g.configuredAccounts = 8 // Four configured peers have not activated yet.
	if g.beginRekey(lanes[1]) {
		t.Fatal("readiness counted only attached owners instead of the configured pool")
	}
}
