package tunnel

import (
	"context"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Observed DEV6 historical reservations, after subtracting current-session
// allocations. This is a capacity sensitivity case, not a field replay: the
// raw capture does not contain the original grant records or provider timing.
var dev7HistoricalDebt = []pool.Amount{
	{Bytes: 10474874, Messages: 173}, {Bytes: 10976200, Messages: 154},
	{Bytes: 10450688, Messages: 152}, {Bytes: 10240547, Messages: 166},
	{Bytes: 9536885, Messages: 171}, {Bytes: 10377791, Messages: 173},
	{Bytes: 10383225, Messages: 170}, {Bytes: 11193823, Messages: 165},
	{Bytes: 8785433, Messages: 150}, {Bytes: 9029531, Messages: 140},
	{Bytes: 9872467, Messages: 161}, {Bytes: 9521350, Messages: 151},
	{Bytes: 10780973, Messages: 172}, {Bytes: 8565977, Messages: 149},
	{Bytes: 9844240, Messages: 164},
}

func dev7RetainedFieldState(t *testing.T) func(string, string, servercfg.Config, servercfg.User) {
	return func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
		for i, a := range cfg.Accounts {
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			debt := dev7HistoricalDebt[i%len(dev7HistoricalDebt)]
			grants := 1
			if os.Getenv("ITOM_LOAD_MANY_RETAINED_GRANTS") == "1" {
				// DEV7 field diagnostics show dozens of missing-wallet grants on
				// every account. Preserve the exact same aggregate charge while
				// exercising the reconciliation traffic absent from the one-grant
				// capacity model. No invented spend or release proof is supplied.
				grants = 48
			}
			for remaining := uint64(grants); remaining > 0; remaining-- {
				part := pool.Amount{Bytes: debt.Bytes / remaining, Messages: debt.Messages / remaining}
				if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, part); err != nil {
					t.Fatal(err)
				}
				debt.Bytes -= part.Bytes
				debt.Messages -= part.Messages
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Opt-in diagnostic uses the SAME measured-byte implementation and assertions
// as the sustained qualification. Durations shorter than 60s are exploratory
// measurements only, never accepted as sustained qualification evidence.
func TestDEV7FieldCapacitySensitivity(t *testing.T) {
	if os.Getenv("ITOM_DEV7_FIELD_CAPACITY") != "1" {
		t.Skip("opt-in field-capacity experiment")
	}
	testSupervisedProductionLoad(t)
}

func TestDEV7RetainedHeadroomFitsTwoBulkCopiesAndCompleteFastBody(t *testing.T) {
	for _, index := range []int{4, 10, 11, 14} {
		t.Run(string(rune('a'+index)), func(t *testing.T) {
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
			s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			old, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, dev7HistoricalDebt[index])
			if err != nil {
				t.Fatal(err)
			}
			l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
			establishRecoveredServer(t, l)
			if err = l.offer(); err != nil {
				t.Fatal(err)
			}
			fast, bulk := l.issuedGrantCounts()
			if fast != 1 || bulk != 2 {
				t.Fatal("inbound two-body capacity stranded", fast, bulk)
			}
			var local []journal.Grant
			for i := 0; i < 2; i++ {
				g, e := l.tryLocalGrant(dataBatchMIMEHardCap, false)
				if e != nil {
					t.Fatal("outbound two-body capacity stranded", i, e)
				}
				local = append(local, g)
			}
			g, e := l.tryLocalGrant(160<<10, true)
			if e != nil {
				t.Fatal("complete FAST body cannot fit alongside bulk", e)
			}
			local = append(local, g)
			for _, g := range local {
				if err = s.CloseGrant(g.Ref); err != nil {
					t.Fatal(err)
				}
			}
			if got, e := s.GetGrant(old.Ref); e != nil || got != old {
				t.Fatal("historical allocation changed", e)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
