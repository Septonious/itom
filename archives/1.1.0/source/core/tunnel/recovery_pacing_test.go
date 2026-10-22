package tunnel

import (
	"context"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestRecoveryPacingBoundsMissingWalletAuditWithoutForgivingDebt(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 64 << 10, Messages: 1}); err != nil {
			t.Fatal(err)
		}
	}
	before, err := s.Held(a.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), urgentQueue: make(chan queuedControl, 32), maintenanceQueue: make(chan queuedControl, 32)}
	now := time.Unix(1000, 0)
	seen := map[wire.QuotaKey]bool{}
	for round := 0; round < 3; round++ {
		at := now.Add(time.Duration(round) * 15 * time.Second)
		if err = l.reconcileUnpublishedGrantsAt(at); err != nil {
			t.Fatal(err)
		}
		if len(l.urgentQueue) != 4 {
			t.Fatal("lane-wide audit cap", round, len(l.urgentQueue))
		}
		for len(l.urgentQueue) > 0 {
			f := <-l.urgentQueue
			c, e := wire.DecodeControl(f.Frame)
			if e != nil {
				t.Fatal(e)
			}
			g := c.(wire.GrantRecovery)
			if seen[g.Offer.Key] {
				t.Fatal("oldest audit rotation starved unseen grants")
			}
			seen[g.Offer.Key] = true
		}
		for i := 0; i < 100; i++ {
			if err = l.reconcileUnpublishedGrantsAt(at.Add(time.Millisecond * time.Duration(i))); err != nil {
				t.Fatal(err)
			}
		}
		if len(l.urgentQueue) != 0 {
			t.Fatal("frequent offer calls flooded recovery queue")
		}
	}
	after, err := s.Held(a.ID, u.ID)
	if err != nil || after != before || len(seen) != 12 {
		t.Fatal("timing changed authority", before, after, err, len(seen))
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestThroughputModelShortTransferRampsOnlyWithUsefulProgress(t *testing.T) {
	var m throughputModel
	now := time.Unix(100, 0)
	window := uint64(deliveryInitialWindow)
	for i := 0; i < 4; i++ {
		window = m.next(now.Add(time.Duration(i)*400*time.Millisecond), modelFeedback{deliveryFeedback: deliveryFeedback{Window: window, Flight: window, ACKFlight: window, Demand: true, Delivered: uint64(i+1) << 20}, RTT: 300 * time.Millisecond, RTTAt: now.Add(time.Duration(i) * 400 * time.Millisecond)})
	}
	if window != deliveryMaxWindow {
		t.Fatal("short transfer still spends seven turns discovering flight", window)
	}
	before := window
	for i := 0; i < 8; i++ {
		window = m.next(now.Add(2*time.Second+time.Duration(i)*10*time.Millisecond), modelFeedback{deliveryFeedback: deliveryFeedback{Window: window, Flight: window, Demand: true, Delivered: 4 << 20}})
		if window != before {
			t.Fatal("empty ticks grew or reduced flight without stalled deadline", window, before)
		}
	}
	idle := m.next(now.Add(3*time.Second), modelFeedback{deliveryFeedback: deliveryFeedback{Window: window}})
	if idle != deliveryInitialWindow {
		t.Fatal("bulk ramp leaked flight into idle", idle)
	}
}
