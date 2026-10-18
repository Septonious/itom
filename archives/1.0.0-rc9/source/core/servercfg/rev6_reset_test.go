package servercfg

import (
	"bytes"
	"encoding/json"
	"itom.local/core/mailbox"
	"reflect"
	"testing"
)

func TestRev6AccountsResetForgetsCredentialsAndFencesOldJournals(t *testing.T) {
	c, e := New("provider")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.AddUser("admin"); e != nil {
		t.Fatal(e)
	}
	m := mailbox.Config{Host: "imap.example.org", Port: 993, TLS: "tls", Username: "old-login", Password: "old-secret"}
	old, e := c.AddAccount(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.RemoveAccount(old); e != nil {
		t.Fatal(e)
	}
	m.Username = "active-login"
	if _, e = c.AddAccount(m); e != nil {
		t.Fatal(e)
	}
	users, server := append([]User(nil), c.Users...), c.Server
	if e = c.ResetAccounts(); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(c)
	if len(c.Accounts) != 0 || len(c.Retired) != 0 || !reflect.DeepEqual(users, c.Users) || server != c.Server || bytes.Contains(raw, []byte("old-secret")) || bytes.Contains(raw, []byte("old-login")) || bytes.Contains(raw, []byte("active-login")) {
		t.Fatal("reset retained login or changed clients")
	}
	first, e := c.AddAccount(m)
	if e != nil || first == old || first == "a02" {
		t.Fatal("reused old journal identity", first, e)
	}
	if e = c.RemoveAccount(first); e != nil {
		t.Fatal(e)
	}
	same, e := c.AddAccount(m)
	if e != nil || same != first {
		t.Fatal("normal readd changed identity", same, e)
	}
	if e = c.ResetAccounts(); e != nil {
		t.Fatal(e)
	}
	next, e := c.AddAccount(m)
	if e != nil || next == first {
		t.Fatal("reset reused namespace", next, e)
	}
}
