package journal_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestSessionNamespaceMigrationKeepsAllTenLegacyFoldersAndUnknownDebtInOriginalIssuer(t *testing.T) {
	old := config()
	for _, base := range []string{"fast", "bulk"} {
		for n := 0; n < 4; n++ {
			old.Accounts[0].Users[0].Folders = append(old.Accounts[0].Users[0].Folders, fmt.Sprintf("%s-data-%02d", base, n))
		}
	}
	path := filepath.Join(t.TempDir(), "original.db")
	s, err := journal.Open(path, old, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	enable(t, s)
	g, err := s.Issue(journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 9000, Messages: 9})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.PrepareStarted(g.Ref, "bulk-data-03", wire.ID{2}, 4000, []byte("old physical ciphertext"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Complete(a.Ref, journal.Uncertain); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = journal.Open(path, old, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Config()
	next := s.Config()
	next.Accounts[0].Users[0].Folders = append(next.Accounts[0].Users[0].Folders, "fast-session-v2", "bulk-session-v2")
	held, _ := s.Held("account1", "12345678")
	if err = s.ExtendSessionFolders(next); err != nil {
		t.Fatal(err)
	}
	after, err := s.Held("account1", "12345678")
	if err != nil || after != held {
		t.Fatal("migration changed debt", held, after, err)
	}
	if s.Config().Accounts[0].Limit != before.Accounts[0].Limit || len(s.Config().Accounts[0].Users[0].Folders) != 12 {
		t.Fatal("capacity or old folders changed")
	}
	s.Close()
	s, err = journal.OpenCompatible(path, next, []journal.Config{old}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetAttempt(a.Ref)
	if err != nil || got.Ref != a.Ref || got.State != journal.Unknown {
		t.Fatal("original issuer/attempt forgotten", got, err)
	}
	if _, err = s.Issue(g.Ref.Scope, pool.Amount{Bytes: 1, Messages: 1}); !errors.Is(err, journal.ErrRecovery) {
		t.Fatal("migration bypassed startup inventory", err)
	}
	enable(t, s)
	if _, err = s.Issue(g.Ref.Scope, pool.Amount{Bytes: 1001, Messages: 1}); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("missing history made new capacity", err)
	}
}

func TestSessionNamespaceMigrationRejectsOwnerCapacityAndFolderChanges(t *testing.T) {
	for name, change := range map[string]func(*journal.Config){
		"raise_limit":       func(c *journal.Config) { c.Accounts[0].Limit.Bytes++; c.Accounts[0].Users[0].Limit.Bytes++ },
		"change_provider":   func(c *journal.Config) { c.Accounts[0].Provider = "foreign" },
		"change_user":       func(c *journal.Config) { c.Accounts[0].Users[0].ID = "87654321" },
		"remove_old_folder": func(c *journal.Config) { c.Accounts[0].Users[0].Folders = []string{"fast", "fast-session-v2"} },
		"foreign_folder": func(c *journal.Config) {
			c.Accounts[0].Users[0].Folders = append(c.Accounts[0].Users[0].Folders, "foreign-session-v2")
		},
		"nested_namespace": func(c *journal.Config) {
			c.Accounts[0].Users[0].Folders = append(c.Accounts[0].Users[0].Folders, "fast-session-v2-session-v2")
		},
		"shard_namespace": func(c *journal.Config) {
			c.Accounts[0].Users[0].Folders = append(c.Accounts[0].Users[0].Folders, "fast-data-00", "fast-data-00-session-v2")
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := open(t, filepath.Join(t.TempDir(), "journal.db"))
			defer s.Close()
			before := s.Config()
			next := s.Config()
			next.Accounts[0].Users[0].Folders = append(next.Accounts[0].Users[0].Folders, "fast-session-v2", "bulk-session-v2")
			change(&next)
			if err := s.ExtendSessionFolders(next); err == nil {
				t.Fatal("unsafe migration accepted")
			}
			if !reflect.DeepEqual(before, s.Config()) {
				t.Fatal("failed migration changed config")
			}
		})
	}
}
