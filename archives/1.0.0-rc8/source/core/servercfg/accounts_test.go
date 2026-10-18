package servercfg

import (
	"itom.local/core/mailbox"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveReaddPreservesJournalIdentityAndLegacyConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.db")
	mail := mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "one", Password: "secret"}
	if err := Update(p, func(c *Config) error {
		n, err := New("provider")
		*c = n
		c.Accounts = []Account{{ID: "a01", Mail: mail}, {ID: "a02", Mail: mail}}
		c.Accounts[1].Mail.Username = "two"
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Existing schema-1 data had no retired_accounts: removal upgrades lazily.
	if err := Update(p, func(c *Config) error { return c.RemoveAccount("a01") }); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || len(c.Accounts) != 1 || len(c.Retired) != 1 || c.Schema != 2 {
		t.Fatal(c, err)
	}
	third := mail
	third.Username = "three"
	id, err := c.AddAccount(third)
	if err != nil || id != "a03" {
		t.Fatal(id, err)
	}
	mail.Password = "new-secret"
	id, err = c.AddAccount(mail)
	if err != nil || id != "a01" || len(c.Retired) != 0 {
		t.Fatal(id, err)
	}
	if _, err = c.AddAccount(mail); err == nil {
		t.Fatal("duplicate active account accepted")
	}
	if len(c.Accounts) != 3 {
		t.Fatal("failed add mutated accounts")
	}
	if err = c.RemoveAccount("missing"); err == nil {
		t.Fatal("unknown account removed")
	}
	for _, id := range []string{"a02", "a03", "a01"} {
		if err = c.RemoveAccount(id); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.Accounts) != 0 || c.Validate() != nil {
		t.Fatal(c)
	}
	if err = Update(p, func(out *Config) error { *out = c; return nil }); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p)
	if err != nil {
		t.Fatal(err)
	}
	id, err = c.AddAccount(mail)
	if err != nil || id != "a01" {
		t.Fatal(id, err)
	}
	// No separate secret-bearing tombstone or deleted journal files are created.
	files, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
}
