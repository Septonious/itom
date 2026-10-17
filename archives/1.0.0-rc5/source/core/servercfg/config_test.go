package servercfg

import (
	"itom.local/core/mailbox"
	"path/filepath"
	"testing"
)

func TestRegistryPersistsUIDAndExportsOwnProfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.db")
	var uid string
	if e := Update(p, func(c *Config) error {
		n, e := New("provider")
		if e != nil {
			return e
		}
		*c = n
		c.Accounts = []Account{{ID: "a01", Mail: mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "one", Password: "secret"}}}
		u, e := c.AddUser("one")
		uid = u.ID
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e := Update(p, func(c *Config) error { c.Users[0].Name = "renamed"; _, e := c.AddUser("two"); return e }); e != nil {
		t.Fatal(e)
	}
	c, e := Load(p)
	if e != nil || c.Users[0].ID != uid || c.Users[0].Up == c.Users[1].Up || c.Users[0].Key == c.Users[1].Key {
		t.Fatal(c, e)
	}
	out := filepath.Join(t.TempDir(), "profile.json")
	if e = c.Export(uid, out); e != nil {
		t.Fatal(e)
	}
	profile, e := ReadProfile(out)
	if e != nil || profile.User.ID != uid || len(profile.Accounts) != 1 {
		t.Fatal(e)
	}
	if e = c.Export(uid, out); e == nil {
		t.Fatal("overwrote profile")
	}
	if _, e = c.AddUser("third"); e == nil {
		t.Fatal("unbounded users")
	}
}
func TestRegistryRejectsMixedEndpointAndPlaintext(t *testing.T) {
	c, e := New("provider")
	if e != nil {
		t.Fatal(e)
	}
	c.Accounts = []Account{{ID: "a01", Mail: mailbox.Config{Host: "imap.one", Port: 993, TLS: "tls", Username: "one", Password: "secret"}}, {ID: "a02", Mail: mailbox.Config{Host: "imap.two", Port: 993, TLS: "tls", Username: "two", Password: "secret"}}}
	if c.Validate() == nil {
		t.Fatal("mixed endpoint")
	}
	c.Accounts = c.Accounts[:1]
	c.Accounts[0].Mail.TLS = "plain"
	if c.Validate() == nil {
		t.Fatal("plaintext")
	}
}
