package servercfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"itom.local/core/mailbox"
)

// Exercise persisted state and both exported profile formats, rather than just
// mirroring the removed account-count predicate.
func TestManyAccountsPersistExportAndRestoreIdentity(t *testing.T) {
	for _, count := range []int{1, 2, 5, 10, 11, 16, 32, 64, 65, 128} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			c, err := New("scale-test")
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= count; i++ {
				id, err := c.AddAccount(mailbox.Config{Host: "imap.example.invalid", Port: 993, TLS: "tls", Username: fmt.Sprintf("mail-%d", i), Password: "synthetic-secret"})
				if err != nil || id != fmt.Sprintf("a%02d", i) {
					t.Fatalf("account %d: id=%s error=%v", i, id, err)
				}
			}
			u, err := c.AddUser("Phone")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			db := filepath.Join(dir, "server.db")
			if err = Update(db, func(saved *Config) error { *saved = c; return nil }); err != nil {
				t.Fatal(err)
			}
			loaded, err := Load(db)
			if err != nil || len(loaded.Accounts) != count {
				t.Fatalf("load count %d: %v", count, err)
			}
			profile := filepath.Join(dir, "profile.json")
			if err = loaded.Export(u.ID, profile); err != nil {
				t.Fatal(err)
			}
			p, err := ReadProfile(profile)
			if err != nil || len(p.Accounts) != count || p.Accounts[count-1].ID != fmt.Sprintf("a%02d", count) {
				t.Fatalf("profile round trip: %v", err)
			}
			invitation, err := loaded.androidInvitation(u.ID)
			if err != nil {
				t.Fatal(err)
			}
			var android struct {
				Profile struct {
					Accounts []struct {
						ID  string `json:"id"`
						Ref string `json:"credential_ref"`
					} `json:"accounts"`
				} `json:"profile"`
				Credentials map[string]map[string]string `json:"credentials"`
			}
			if err = json.Unmarshal(invitation, &android); err != nil {
				t.Fatal(err)
			}
			if len(android.Profile.Accounts) != count || len(android.Credentials) != count {
				t.Fatal("Android invitation truncated")
			}
			for _, a := range android.Profile.Accounts {
				if android.Credentials[a.Ref]["password"] != "synthetic-secret" {
					t.Fatal("credential association lost")
				}
			}
			last := loaded.Accounts[count-1]
			if err = loaded.RemoveAccount(last.ID); err != nil {
				t.Fatal(err)
			}
			id, err := loaded.AddAccount(last.Mail)
			if err != nil || id != last.ID {
				t.Fatal("retired account was renumbered", err)
			}
			before, _ := json.Marshal(loaded)
			if _, err = loaded.AddAccount(last.Mail); err == nil {
				t.Fatal("duplicate accepted")
			}
			after, _ := json.Marshal(loaded)
			if string(before) != string(after) {
				t.Fatal("failed addition mutated persistent identity")
			}
		})
	}
}

func TestOversizedAccountConfigurationDoesNotPoisonStoredState(t *testing.T) {
	c, _ := New("scale-test")
	_, err := c.AddAccount(mailbox.Config{Host: "imap.example.invalid", Port: 993, TLS: "tls", Username: "one", Password: "synthetic-secret"})
	if err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "server.db")
	if err = Update(db, func(saved *Config) error { *saved = c; return nil }); err != nil {
		t.Fatal(err)
	}
	err = Update(db, func(saved *Config) error {
		for i := 0; i < 260; i++ {
			mail := saved.Accounts[0].Mail
			mail.Username = fmt.Sprintf("large-%d", i)
			mail.Password = strings.Repeat("x", 4096)
			saved.Accounts = append(saved.Accounts, Account{ID: fmt.Sprintf("large%d", i), Mail: mail})
		}
		return nil
	})
	if !errors.Is(err, ErrConfigSize) {
		t.Fatalf("expected size error, got %v", err)
	}
	loaded, err := Load(db)
	if err != nil || loaded.Accounts[0].Mail.Password != "synthetic-secret" {
		t.Fatal("failed transaction poisoned stored configuration", err)
	}
	oversize := filepath.Join(t.TempDir(), "oversize.json")
	if err = os.WriteFile(oversize, []byte(strings.Repeat(" ", MaxConfigBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadProfile(oversize); !errors.Is(err, ErrConfigSize) {
		t.Fatal("oversized profile not classified", err)
	}
}
