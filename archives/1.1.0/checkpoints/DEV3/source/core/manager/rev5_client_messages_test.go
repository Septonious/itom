package manager

import (
	"bytes"
	"itom.local/core/servercfg"
	"testing"
)

func TestRev5MessageMenuTargetsUIDAndGlobalClearsOverrides(t *testing.T) {
	u, a, b := clientUI(t, "3\n2\n1\nPersonal message\n")
	if err := u.serverMessage(); err != nil {
		t.Fatal(err)
	}
	c, err := servercfg.Load(u.db())
	if err != nil || c.ClientNotices[b.ID] != "Personal message" || c.NoticeForClient(a.ID).Text != "" {
		t.Fatal("wrong client selected", err)
	}
	var out bytes.Buffer
	if err := ServerMessage(u.State, "Global update", true, &out); err != nil {
		t.Fatal(err)
	}
	c, err = servercfg.Load(u.db())
	if err != nil || len(c.ClientNotices) != 0 || c.NoticeForClient(b.ID).Text != "Global update" {
		t.Fatal("global did not clear personal message", err)
	}
	if err := ClientMessage(u.State, b.ID, "", false, &out); err != nil {
		t.Fatal(err)
	}
	before := c.Notice.Revision
	if err := ClientMessage(u.State, "00000000", "wrong", true, &out); err == nil {
		t.Fatal("unknown UID accepted")
	}
	c, _ = servercfg.Load(u.db())
	if c.Notice.Revision != before {
		t.Fatal("invalid UID changed global revision")
	}
}
