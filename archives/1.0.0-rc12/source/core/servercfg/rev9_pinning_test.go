package servercfg

import (
	"encoding/json"
	"itom.local/core/mailbox"
	"testing"
)

func TestRev9PinExportAndClearKeepsAccountIdentity(t *testing.T) {
	c, _ := New("rambler")
	id, err := c.AddAccount(mailbox.Config{Host: "imap.example", Port: 993, TLS: "tls", Username: "test", Password: "test"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := c.AddUser("Phone")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetAccountDialIP("", "81.19.77.171"); err != nil {
		t.Fatal(err)
	}
	raw, err := c.androidInvitation(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var invitation struct {
		Profile struct {
			Accounts []struct {
				ID     string `json:"id"`
				Host   string `json:"host"`
				DialIP string `json:"dial_ip"`
				Verify bool   `json:"verify_certificate"`
			} `json:"accounts"`
		} `json:"profile"`
	}
	if err = json.Unmarshal(raw, &invitation); err != nil {
		t.Fatal(err)
	}
	a := invitation.Profile.Accounts[0]
	if a.ID != id || a.Host != "imap.example" || a.DialIP != "81.19.77.171" || !a.Verify {
		t.Fatal(a)
	}
	if err = c.SetAccountDialIP(id, "bad.example"); err == nil || c.Accounts[0].Mail.DialIP != "81.19.77.171" {
		t.Fatal("invalid pin mutated account", err)
	}
	if err = c.SetAccountDialIP(id, ""); err != nil || c.Accounts[0].ID != id || c.Accounts[0].Mail.DialIP != "" {
		t.Fatal("clear changed identity", err)
	}
}
