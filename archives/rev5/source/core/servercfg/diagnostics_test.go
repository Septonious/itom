package servercfg

import (
	"bytes"
	"itom.local/core/mailbox"
	"os"
	"path/filepath"
	"testing"
)

func TestServerDiagnosticsPersistsAndDoesNotEnterClientProfile(t *testing.T) {
	cfg, err := New("test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := cfg.AddUser("test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = []Account{{ID: "a01", Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "u", Password: "p"}}}
	cfg.DiagnosticsMode = "off"
	dir := t.TempDir()
	db := filepath.Join(dir, "server.db")
	if err = Update(db, func(c *Config) error { *c = cfg; return nil }); err != nil {
		t.Fatal(err)
	}
	got, err := Load(db)
	if err != nil || got.DiagnosticsMode != "off" {
		t.Fatal(got.DiagnosticsMode, err)
	}
	profile := filepath.Join(dir, "profile.json")
	if err = got.Export(u.ID, profile); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(profile)
	if err != nil || bytes.Contains(raw, []byte("diagnostics_mode")) {
		t.Fatal("server setting leaked into Android", err)
	}
	if _, err = ReadProfile(profile); err != nil {
		t.Fatal(err)
	}
	cfg.DiagnosticsMode = "invalid"
	if cfg.Validate() == nil {
		t.Fatal("invalid mode accepted")
	}
	cfg.DiagnosticsMode = ""
	if cfg.Validate() != nil {
		t.Fatal("old config default rejected")
	}
}
