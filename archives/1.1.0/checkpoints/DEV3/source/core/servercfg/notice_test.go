package servercfg

import "testing"

func TestServerMessageUpdateIsIdempotent(t *testing.T) {
	c, err := New("provider")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.SetNotice("hello"); err != nil {
		t.Fatal(err)
	}
	if err = c.SetNotice("hello"); err != nil || c.Notice.Revision != 1 {
		t.Fatal("same message generated a new revision", err)
	}
	if err = c.SetNotice(""); err != nil || c.Notice.Revision != 2 {
		t.Fatal("message clearing not revisioned", err)
	}
	old := c.Notice
	if c.SetNotice("bad\x00message") == nil || c.Notice != old {
		t.Fatal("invalid message altered saved state")
	}
}
