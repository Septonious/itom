package servercfg

import (
	"encoding/base64"
	"encoding/json"
	"itom.local/core/mailbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAndroidInvitationMatchesUserFoldersAndCredentials(t *testing.T) {
	c, _ := New("rambler")
	c.Accounts = []Account{{ID: "a01", Mail: mailbox.Config{Host: "imap.example", Port: 993, TLS: "tls", Username: "mail@example", Password: "secret"}}}
	u, e := c.AddUser("Phone")
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "invitation.json")
	if e = c.ExportAndroid(u.ID, path); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(path)
	var i struct {
		Schema  int `json:"schema_version"`
		Wire    int `json:"wire_version"`
		Profile struct {
			UID       string `json:"user_id"`
			Namespace string `json:"mailbox_namespace"`
			Accounts  []struct {
				Ref string `json:"credential_ref"`
			} `json:"accounts"`
		} `json:"profile"`
		Credentials map[string]map[string]string `json:"credentials"`
	}
	if e = json.Unmarshal(raw, &i); e != nil {
		t.Fatal(e)
	}
	if i.Schema != 2 || i.Wire != 2 || i.Profile.UID != u.ID || i.Profile.Namespace+"-up" != u.Up || i.Profile.Namespace+"-down" != u.Down || len(i.Profile.Accounts) != 1 || i.Credentials[i.Profile.Accounts[0].Ref]["username"] != "mail@example" {
		t.Fatal("wrong Android invitation")
	}
	link, e := c.AndroidLink(u.ID)
	if e != nil || !strings.HasPrefix(link, "itom://import#") || strings.ContainsAny(link, "\r\n") {
		t.Fatal("invalid one-line link", e)
	}
	decoded, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(link, "itom://import#"))
	if e != nil {
		t.Fatal(e)
	}
	var linked map[string]any
	if e = json.Unmarshal(decoded, &linked); e != nil {
		t.Fatal(e)
	}
	profile := linked["profile"].(map[string]any)
	account := profile["accounts"].([]any)[0].(map[string]any)
	if account["name"] != "mail@example" || account["id"] != "a01" {
		t.Fatal("email display or durable identity changed")
	}
	if linked["credentials"].(map[string]any)[account["credential_ref"].(string)].(map[string]any)["password"] != "secret" {
		t.Fatal("link lost credential")
	}
	if e = c.ExportAndroid(u.ID, path); e == nil {
		t.Fatal("overwrote an existing profile")
	}
}
