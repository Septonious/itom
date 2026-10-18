package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// FIELD12 client run 1791217675382197528: a02/a06/a08 remain in
// grants; seven accounts mostly have only 512KiB/32 fast offers. Client logs
// cannot identify the exact server debt. This reproduces a sufficient cause:
// lost publication reserves all old message slots, with no wallet entry to
// replay. It tests real durable state and the production supervisors, not a
// pre-selected pool of already-ready lanes.
func fieldLegacyState(t *testing.T) func(string, string, servercfg.Config, servercfg.User) {
	t.Helper()
	return func(serverDir, clientDir string, cfg servercfg.Config, u servercfg.User) {
		for _, a := range cfg.Accounts {
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile8), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			wd := filepath.Join(clientDir, a.ID)
			if err = os.MkdirAll(wd, 0700); err != nil {
				t.Fatal(err)
			}
			w, err := wallet.Open(filepath.Join(wd, "wallet.db"), u.ID, s.StoreID())
			if err != nil {
				t.Fatal(err)
			}
			if a.ID == "a02" || a.ID == "a06" || a.ID == "a08" {
				for i := 0; i < 4; i++ {
					_, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, pool.Amount{Bytes: fastLeaseBytes, Messages: fastLeaseMessages})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = w.Close(); err != nil {
				t.Fatal(err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFieldLegacyGrantPublicationDoesNotStrandThreeAccounts(t *testing.T) {
	// Advertised BINARY must not move startup inventory off physical BODY.
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "15")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	f := newProductionSupervisorFixtureState(t, 10, fieldLegacyState(t))
	for i := 1; i <= 10; i++ {
		l := f.waitLane(t, fmt.Sprintf("a%02d", i), 0)
		// Publication readiness does not freeze permit queues: DATA workers
		// may consume them immediately. Require bounded replenishment of both
		// classes, then the original account-specific live TCP echo.
		deadline := time.Now().Add(5 * time.Second)
		for (len(l.grants) == 0 || len(l.fastGrants) == 0) && time.Now().Before(deadline) {
			if err := l.ctx.Err(); err != nil { t.Fatal("carrier lost during grant refill", err) }
			time.Sleep(25 * time.Millisecond)
		}
		if len(l.grants) == 0 || len(l.fastGrants) == 0 {
			t.Fatalf("%s grant refill stalled: bulk=%d fast=%d", l.account.ID, len(l.grants), len(l.fastGrants))
		}
		p := f.openOn(t, l.account.ID)
		if err := supervisorEcho(p, i); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("installed_profile=8MiB/128 old_publications_lost_accounts=3/10 both_classes_ready=10/10 actual_TCP_echo=true")
}

// FIELD12 retransmitted1049 usage records for640 APPENDs. Registration ACK
// must advance before physical GC: withholding it makes a delayed cleaner
// amplify pending descriptors on every retry. The ACK must NEVER refund debt.
func TestFieldDescriptorRegistrationAcknowledgedBeforeBodyOrCleanup(t *testing.T) {
	a := servercfg.Account{ID: "a01"}
	a.Mail.Host = "mail.example"
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	cipher := cleanupFixtureEpoch(t)
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), s.StoreID(), cipher)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, pool.Amount{Bytes: fastLeaseBytes, Messages: fastLeaseMessages})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cipher, reports: map[[32]byte]wire.UsageReport{}, pending: map[[32]byte]mailbox.Message{}, urgentQueue: make(chan queuedControl, 32), maintenanceQueue: make(chan queuedControl, 32), controlQueue: make(chan queuedControl, 32)}
	establishRecoveredServer(t, l)
	before, _ := s.Held(a.ID, u.ID)
	for i := 0; i < 3; i++ {
		r, _, err := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte{byte(i + 1)}, 256), func(b []byte) (uint64, error) { return uint64(len(b) + 100), nil })
		if err != nil {
			t.Fatal(err)
		}
		f, err := wire.EncodeControl(0, r)
		if err != nil {
			t.Fatal(err)
		}
		if err = l.process(f); err != nil {
			t.Fatal(err)
		}
	}
	var acknowledgements int
	for _, q := range []chan queuedControl{l.urgentQueue, l.maintenanceQueue, l.controlQueue} {
		for len(q) > 0 {
			c, err := wire.DecodeControl(l.resolveQuotaControl(<-q).Frame)
			if err != nil {
				t.Fatal(err)
			}
			status, ok := c.(wire.CleanupStatus)
			if !ok {
				continue
			}
			if status.Released != (wire.Quota{}) || status.Closed {
				t.Fatal("registration fabricated cleanup", status)
			}
			if err = w.Acknowledge(status); err != nil {
				t.Fatal(err)
			}
			acknowledgements++
		}
	}
	entries, err := w.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if acknowledgements == 0 || len(entries) != 1 || len(entries[0].Pending) != 0 || entries[0].Ack.Messages != 3 {
		t.Fatalf("unacknowledged descriptor prefix: ACKs=%d entries=%+v", acknowledgements, entries)
	}
	after, _ := s.Held(a.ID, u.ID)
	if after != before {
		t.Fatal("ACK refunded physical quota", before, after)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
