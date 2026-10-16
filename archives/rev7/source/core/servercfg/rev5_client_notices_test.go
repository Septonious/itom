package servercfg

import (
	"itom.local/core/wire"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRev5IndividualMessagesOverrideAndGlobalUpdateClearsAll(t *testing.T) {
	c, _ := New("messages")
	a, _ := c.AddUser("same")
	b, _ := c.AddUser("same")
	if err := c.SetNotice("global"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetClientNotice(a.ID, "individual A"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetClientNotice(b.ID, "individual B"); err != nil {
		t.Fatal(err)
	}
	if c.NoticeForClient(a.ID).Text != "individual A" || c.NoticeForClient(b.ID).Text != "individual B" || c.Notice.Text != "global" {
		t.Fatal("override precedence")
	}
	db := filepath.Join(t.TempDir(), "server.db")
	if err := Update(db, func(saved *Config) error { *saved = c; return nil }); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(db)
	if err != nil || !reflect.DeepEqual(loaded.ClientNotices, c.ClientNotices) {
		t.Fatal("override persistence", err)
	}
	revision := c.Notice.Revision
	if err := c.SetClientNotice(a.ID, ""); err != nil || c.NoticeForClient(a.ID).Text != "global" || c.Notice.Revision != revision+1 {
		t.Fatal("override removal must send a newer global view", err)
	}
	if err := c.SetNotice("new global"); err != nil || len(c.ClientNotices) != 0 {
		t.Fatal("global update retained individual entries", err)
	}
	if c.NoticeForClient(a.ID).Text != "new global" || c.NoticeForClient(b.ID).Text != "new global" {
		t.Fatal("global not effective for all clients")
	}
	if err := c.SetClientNotice(a.ID, "temporary"); err != nil {
		t.Fatal(err)
	}
	revision = c.Notice.Revision
	if err := c.SetNotice("new global"); err != nil || c.Notice.Revision != revision+1 || len(c.ClientNotices) != 0 {
		t.Fatal("re-saving global failed to clear override", err)
	}
	if err := c.SetClientNotice(b.ID, "temporary"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetNotice(""); err != nil || len(c.ClientNotices) != 0 || c.NoticeForClient(b.ID).Text != "" {
		t.Fatal("clearing global retained override", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRev5InvalidClientMessageCannotMutateOrLeaveRemovedIdentity(t *testing.T) {
	c, _ := New("messages")
	a, _ := c.AddUser("client")
	_ = c.SetClientNotice(a.ID, "private")
	before := c.Notice
	for _, tc := range []struct{ uid, text string }{{"00000000", "unknown"}, {a.ID, "bad\x00text"}} {
		if err := c.SetClientNotice(tc.uid, tc.text); err == nil || c.Notice != before || c.ClientNotices[a.ID] != "private" {
			t.Fatal("invalid update changed state", err)
		}
	}
	c.Notice.Revision = wire.MaxOffset
	if err := c.SetNotice("replacement"); err == nil || c.ClientNotices[a.ID] != "private" {
		t.Fatal("revision overflow cleared overrides")
	}
	c.Notice.Revision = before.Revision
	if err := c.RemoveUser(a.ID); err != nil || len(c.ClientNotices) != 0 {
		t.Fatal("removed client left an override", err)
	}
}
