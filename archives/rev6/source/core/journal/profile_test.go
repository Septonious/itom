package journal

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func largerProfile(c Config) Config {
	c, _, _ = canonicalConfig(c)
	for i := range c.Accounts {
		c.Accounts[i].Limit.Bytes *= 2
		c.Accounts[i].Limit.Messages *= 2
		for j := range c.Accounts[i].Users {
			c.Accounts[i].Users[j].Limit.Bytes *= 2
			c.Accounts[i].Users[j].Limit.Messages *= 2
		}
	}
	return c
}

func TestCompatibleProfileRetainsDirtyDebtAndRejectsForeignScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old := testConfig(1)
	next := largerProfile(old)
	s := openTest(t, path, old)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	for i := 0; i < 4; i++ {
		a := prepareTest(t, s, g)
		if i > 0 {
			if _, e := s.Start(a.Ref); e != nil {
				t.Fatal(e)
			}
		}
		if i == 2 {
			if e := s.Complete(a.Ref, Uncertain); e != nil {
				t.Fatal(e)
			}
		}
		if i == 3 {
			if e := s.Observe(a.Ref, s.Run(), observation()); e != nil {
				t.Fatal(e)
			}
		}
	}
	r := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 3000, Messages: 3})
	usage, _ := descriptor(t, r, 1, 0)
	applyTest(t, s, usage)
	applyTest(t, s, wire.StorageRelease{Key: quotaKey(r), Final: usage.Total})
	attempts, _ := s.ScanAttempts(0, 128)
	grants, _ := s.ScanGrants(0, 64)
	debt, _ := s.Held("a0", "12345678")
	id, run := s.StoreID(), s.Run()
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Accounts[0].ID = "foreign" },
		func(c *Config) { c.Accounts[0].Provider = "foreign" },
		func(c *Config) { c.Accounts[0].Users[0].ID = "23456789" },
		func(c *Config) { c.Accounts[0].Users[0].Folders[0] = "foreign-folder" },
		func(c *Config) { c.Accounts[0].Limit.Bytes++ },
	} {
		foreign, _, _ := canonicalConfig(old)
		mutate(&foreign)
		if x, e := OpenCompatible(path, next, []Config{foreign}, time.Second); e == nil {
			x.Close()
			t.Fatal("foreign profile accepted")
		}
	}
	for reopen := 0; reopen < 2; reopen++ {
		s, e := OpenCompatible(path, next, []Config{old}, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if !s.Existing() || s.StoreID() != id || s.Run() != run+uint64(reopen)+1 {
			t.Fatal("identity/run reset")
		}
		if !reflect.DeepEqual(s.Config(), canonicalTest(old)) {
			t.Fatal("profile raised")
		}
		got, _ := s.ScanAttempts(0, 128)
		for i := range attempts {
			if attempts[i].State == Started {
				attempts[i].State = Unknown
			}
		}
		if !reflect.DeepEqual(got, attempts) {
			t.Fatalf("attempt identity/location/digest lost: got=%v expected=%v", got, attempts)
		}
		nowGrants, _ := s.ScanGrants(0, 64)
		if !reflect.DeepEqual(nowGrants, grants) {
			t.Fatal("grants reset")
		}
		heldTest(t, s, "a0", "12345678", debt)
		if _, e = s.Start(attempts[0].Ref); !errors.Is(e, ErrGeneration) {
			t.Fatalf("old writer revived: %v", e)
		}
		if _, e = s.Issue(g.Ref.Scope, pool.Amount{Bytes: 1, Messages: 1}); !errors.Is(e, ErrRecovery) {
			t.Fatalf("inventory gate skipped: %v", e)
		}
		s.Close()
	}
}

func canonicalTest(c Config) Config { c, _, _ = canonicalConfig(c); return c }

func TestProfileMigrationAtomicAuditedIdempotentAndBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old := testConfig(1)
	next := largerProfile(old)
	s := openTest(t, path, old)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	s.Start(a.Ref)
	s.Complete(a.Ref, Uncertain)
	if e := s.MigrateConfig(next); !errors.Is(e, ErrState) {
		t.Fatalf("live migration accepted: %v", e)
	}
	s.Close()
	s = openTest(t, path, old)
	before, _ := s.ScanAttempts(0, 128)
	held, _ := s.Held("a0", "12345678")
	if e := s.MigrateConfig(next); e != nil {
		t.Fatal(e)
	}
	after, _ := s.ScanAttempts(0, 128)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("migration changed debt records")
	}
	heldTest(t, s, "a0", "12345678", held)
	record, e := s.LastMigration()
	if e != nil || record.Sequence != 1 || record.Attempts != 1 || record.Grants != 1 || record.Outcome != "audited" {
		t.Fatal(record, e)
	}
	if e = s.MigrateConfig(next); e != nil {
		t.Fatal(e)
	}
	duplicate, _ := s.LastMigration()
	if record != duplicate {
		t.Fatal("idempotent run changed audit")
	}
	if e = s.MigrateConfig(old); e != nil {
		t.Fatal("safe downgrade:", e)
	}
	record, _ = s.LastMigration()
	if record.Sequence != 2 {
		t.Fatal(record)
	}
	s.Close()
	if x, e := Open(path, next, time.Second); !errors.Is(e, ErrConfig) {
		if x != nil {
			x.Close()
		}
		t.Fatalf("strict opener accepted changed config: %v", e)
	}
	s = openTest(t, path, old)
	heldTest(t, s, "a0", "12345678", held)
}

func TestProfileDowngradeDeniesByteMessageAndForeignConfigurations(t *testing.T) {
	for _, debt := range []pool.Amount{{Bytes: 15000, Messages: 1}, {Bytes: 1000, Messages: 15}} {
		t.Run("debt", func(t *testing.T) {
			old := testConfig(1)
			next := largerProfile(old)
			path := filepath.Join(t.TempDir(), "s.db")
			s := openTest(t, path, next)
			issueTest(t, s, "a0", "12345678", debt)
			s.Close()
			s = openTest(t, path, next)
			if e := s.MigrateConfig(old); !errors.Is(e, ErrLimit) {
				t.Fatalf("unsafe downgrade: %v", e)
			}
			heldTest(t, s, "a0", "12345678", debt)
			foreign := canonicalTest(next)
			foreign.Accounts[0].Users[0].Folders[0] = "foreign"
			if e := s.MigrateConfig(foreign); !errors.Is(e, ErrScope) {
				t.Fatalf("foreign migration: %v", e)
			}
			if !reflect.DeepEqual(s.Config(), canonicalTest(next)) {
				t.Fatal("failed transition changed config")
			}
		})
	}
}

func TestProfileMigrationRejectsCorruptDebt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	old := testConfig(1)
	s := openTest(t, path, old)
	if e := s.db.Update(func(tx *bolt.Tx) error { return setBalance(tx, ownerKey("a0", "12345678"), pool.Amount{Bytes: 1}) }); e != nil {
		t.Fatal(e)
	}
	if e := s.MigrateConfig(largerProfile(old)); !errors.Is(e, ErrCorrupt) {
		t.Fatal(e)
	}
	if _, e := s.Held("a0", "12345678"); !errors.Is(e, ErrPoisoned) {
		t.Fatal(e)
	}
}

func TestProfileMigrationCrashChild(t *testing.T) {
	path := os.Getenv("ITOM_PROFILE_CRASH_PATH")
	if path == "" {
		return
	}
	s := openTest(t, path, testConfig(1))
	if os.Getenv("ITOM_PROFILE_CRASH_STAGE") == "before" {
		s.beforeCommit = func() error { os.Exit(0); return nil }
	} else {
		s.afterCommit = func() error { os.Exit(0); return nil }
	}
	if e := s.MigrateConfig(largerProfile(testConfig(1))); e != nil {
		t.Fatal(e)
	}
	t.Fatal("crash hook did not run")
}

func TestProfileMigrationProcessDeathAndLostAcknowledgement(t *testing.T) {
	for _, stage := range []string{"before", "after", "lost-ack"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "s.db")
			old := testConfig(1)
			next := largerProfile(old)
			s := openTest(t, path, old)
			g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
			a := prepareTest(t, s, g)
			s.Start(a.Ref)
			id := s.StoreID()
			s.Close()
			if stage == "lost-ack" {
				s = openTest(t, path, old)
				s.afterCommit = func() error { return errors.New("lost commit acknowledgement") }
				if e := s.MigrateConfig(next); e == nil {
					t.Fatal("missing IO error")
				}
				if e := s.Audit(); !errors.Is(e, ErrPoisoned) {
					t.Fatal(e)
				}
				s.Close()
			} else {
				cmd := exec.Command(os.Args[0], "-test.run=^TestProfileMigrationCrashChild$")
				cmd.Env = append(os.Environ(), "ITOM_PROFILE_CRASH_PATH="+path, "ITOM_PROFILE_CRASH_STAGE="+stage)
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("child: %v %s", e, out)
				}
			}
			s, e := OpenCompatible(path, next, []Config{old}, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			expected := next
			if stage == "before" {
				expected = old
			}
			if !reflect.DeepEqual(s.Config(), canonicalTest(expected)) || s.StoreID() != id {
				t.Fatal("mixed profile or lost identity")
			}
			rows, e := s.ScanAttempts(0, 128)
			if e != nil || len(rows) != 1 || rows[0].State != Unknown || rows[0].Ref != a.Ref {
				t.Fatal(rows, e)
			}
			heldTest(t, s, "a0", "12345678", g.Limit)
			if e = s.MigrateConfig(next); e != nil {
				t.Fatal(e)
			}
			record, e := s.LastMigration()
			if e != nil || record.Sequence != 1 {
				t.Fatal(record, e)
			}
		})
	}
}
