package main

import (
	"itom.local/core/servercfg"
	"path/filepath"
	"testing"
)

func TestRev5MessageCommandUIDAndGlobalReset(t *testing.T) {
	state := t.TempDir()
	db := filepath.Join(state, "server.db")
	c, _ := servercfg.New("messages")
	a, _ := c.AddUser("client A")
	b, _ := c.AddUser("client B")
	if err := servercfg.Update(db, func(out *servercfg.Config) error { *out = c; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"message", "--state", state, "--uid", a.ID, "--text", "&cHello &aWorld"}); err != nil {
		t.Fatal(err)
	}
	saved, err := servercfg.Load(db)
	if err != nil || saved.NoticeForClient(a.ID).Text != "&cHello &aWorld" || saved.NoticeForClient(b.ID).Text != "" {
		t.Fatal("CLI targeted wrong client", err)
	}
	if err := run([]string{"message", "--state", state, "--text", "&eAll clients"}); err != nil {
		t.Fatal(err)
	}
	saved, err = servercfg.Load(db)
	if err != nil || len(saved.ClientNotices) != 0 || saved.NoticeForClient(a.ID).Text != "&eAll clients" {
		t.Fatal("CLI global did not clear override", err)
	}
	if err := run([]string{"message", "--state", state, "--uid", a.ID, "--text", "private"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"message", "--state", state, "--uid", a.ID, "--clear"}); err != nil {
		t.Fatal(err)
	}
	saved, err = servercfg.Load(db)
	if err != nil || len(saved.ClientNotices) != 0 || saved.NoticeForClient(a.ID).Text != "&eAll clients" {
		t.Fatal("CLI individual clear did not restore global", err)
	}
}
