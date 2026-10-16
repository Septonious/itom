package servercfg

import (
	"encoding/json"
	"itom.local/core/mailbox"
	"strings"
	"testing"
)

func TestRemoveUserRevokesProfileAndRetainsIdentity(t *testing.T) {
	c, err := New("provider")
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.AddUser("first")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.AddUser("second")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.AddAccount(mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "one", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.RemoveUser(a.ID); err != nil {
		t.Fatal(err)
	}
	if c.Schema != 3 || len(c.Users) != 1 || c.Users[0] != b || len(c.RetiredUsers) != 1 || c.RetiredUsers[0].Up != a.Up {
		t.Fatal("removal changed unrelated client or lost old scope", c)
	}
	if _, err = c.AndroidLink(a.ID); err == nil {
		t.Fatal("removed credential exported")
	}
	if _, err = c.AndroidLink(b.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(c.RetiredUsers)
	if strings.Contains(string(raw), a.Key) {
		t.Fatal("tombstone retained authentication secret")
	}
	n, err := c.AddUser("replacement")
	if err != nil || n.ID == a.ID || n.Up == a.Up {
		t.Fatal("identity was reused", err)
	}
	_, err = c.AddAccount(mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "two", Password: "secret"})
	if err != nil || c.Schema != 3 {
		t.Fatal("account operation downgraded schema", err)
	}
	before, _ := json.Marshal(c)
	if err = c.RemoveUser(a.ID); err == nil {
		t.Fatal("duplicate removal accepted")
	}
	after, _ := json.Marshal(c)
	if string(before) != string(after) {
		t.Fatal("failed removal mutated config")
	}
	bad := c
	bad.RetiredUsers = append([]UserIdentity(nil), c.RetiredUsers...)
	bad.RetiredUsers[0].ID = n.ID
	if bad.Validate() == nil {
		t.Fatal("active/retired identity collision accepted")
	}
	bad = c
	bad.Schema = 2
	if bad.Validate() == nil {
		t.Fatal("retired identity accepted without schema fence")
	}
}
