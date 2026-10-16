package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"itom.local/core/wire"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
)

func TestLedgerOpensGate19ProfileWithoutReset(t *testing.T) {
	dir := t.TempDir()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	old := pool.Amount{Bytes: 8 << 20, Messages: 128}
	cfg := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: old, Users: []journal.User{{ID: u.ID, Limit: old, Folders: []string{u.Up, u.Down}}}}}}
	s, err := journal.Open(filepath.Join(dir, "data.db"), cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	id, run := s.StoreID(), s.Run()
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = ledger(dir, "data", a, u, false)
	if err != nil {
		t.Fatalf("gate19 existing DATA profile must open conservatively: %v", err)
	}
	defer s.Close()
	if s.StoreID() != id || s.Run() != run+1 {
		t.Fatal("identity/run lost")
	}
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	// A compatible reopen must not silently raise either limit.
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: id}
	if _, err = s.Issue(scope, pool.Amount{Bytes: old.Bytes + 1, Messages: 1}); !errors.Is(err, journal.ErrLimit) {
		t.Fatalf("old byte cap changed: %v", err)
	}
	if _, err = s.Issue(scope, pool.Amount{Bytes: 1024, Messages: old.Messages + 1}); !errors.Is(err, journal.ErrLimit) {
		t.Fatalf("old message cap changed: %v", err)
	}
}

func TestLedgerUpgradeFromActualGate19Files(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	for _, name := range []string{"empty", "dirty"} {
		t.Run(name, func(t *testing.T) {
			var expected struct {
				Store    wire.ID
				Run      uint64
				Attempts []journal.Attempt
				Grants   []journal.Grant
				Held     pool.Amount
			}
			raw, e := os.ReadFile(filepath.Join("testdata", "gate19", name+".json"))
			if e != nil {
				t.Fatal(e)
			}
			if e = json.Unmarshal(raw, &expected); e != nil {
				t.Fatal(e)
			}
			original, e := os.ReadFile(filepath.Join("testdata", "gate19", name+".db"))
			if e != nil {
				t.Fatal(e)
			}
			dir := t.TempDir()
			if e = os.WriteFile(filepath.Join(dir, "data.db"), original, 0600); e != nil {
				t.Fatal(e)
			}
			for reopen := 0; reopen < 2; reopen++ {
				s, e := ledger(dir, "data", a, u, false)
				if e != nil {
					t.Fatal(e)
				}
				if s.StoreID() != expected.Store || s.Run() != expected.Run+uint64(reopen)+1 {
					t.Fatal("gate19 identity/fencing reset")
				}
				held, e := s.Held(a.ID, u.ID)
				if e != nil || held != expected.Held {
					t.Fatal("gate19 debt lost", held, e)
				}
				rows, e := allAttempts(s)
				if e != nil {
					t.Fatal(e)
				}
				for i := range expected.Attempts {
					if expected.Attempts[i].State == journal.Started {
						expected.Attempts[i].State = journal.Unknown
					}
				}
				if !reflect.DeepEqual(rows, expected.Attempts) {
					t.Fatal("gate19 attempt/reference/location/digest changed")
				}
				grants, e := s.ScanGrants(0, 256)
				if e != nil || !reflect.DeepEqual(grants, expected.Grants) {
					t.Fatal("gate19 remote/local grants changed", e)
				}
				limit, e := s.Limit(a.ID, u.ID)
				if e != nil || limit != dataProfile8 {
					t.Fatal("old profile raised")
				}
				if e = s.Audit(); e != nil {
					t.Fatal(e)
				}
				s.Close()
			}
			if e = MigrateDataProfile(dir, a, u, "data12"); e != nil {
				t.Fatal(e)
			}
			s, e := ledger(dir, "data", a, u, false)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			held, e := s.Held(a.ID, u.ID)
			if e != nil || held != expected.Held {
				t.Fatal("migration lost actual gate19 debt", e)
			}
			limit, e := s.Limit(a.ID, u.ID)
			record, _ := s.LastMigration()
			if e != nil || limit != dataProfile12 || record.Sequence != 1 {
				t.Fatal("migration profile/audit", limit, record, e)
			}
		})
	}
}

func TestLegacyProfileReservesFastHeadroom(t *testing.T) {
	dir := t.TempDir()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, e := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile8), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.EnableAfterInventory(a.ID, u.ID, s.Run()); e != nil {
		t.Fatal(e)
	}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{1}}
	if _, e = s.Issue(scope, pool.Amount{Bytes: 7 << 20, Messages: 1}); e != nil {
		t.Fatal(e)
	}
	l := &lane{data: s, account: a, user: u}
	if e = l.issueRemoteGrant(pool.Amount{Bytes: 1 << 20, Messages: 1}, false); !errors.Is(e, journal.ErrLimit) {
		t.Fatalf("old reserve consumed: %v", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, e = l.waitLocalGrantContextClass(ctx, 1<<20, false); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("bulk consumed old fast reserve: %v", e)
	}
	held, e := s.Held(a.ID, u.ID)
	if e != nil || held != (pool.Amount{Bytes: 7 << 20, Messages: 1}) {
		t.Fatal(held, e)
	}
}
