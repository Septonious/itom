package manager

import (
	"itom.local/core/servercfg"
	"testing"
)

func TestRev9ManagerPinAllAndClear(t *testing.T) {
	u, _ := accountUI(t, "7\n2\n81.19.77.171\n")
	if err := u.accounts(); err != nil {
		t.Fatal(err)
	}
	cfg, err := servercfg.Load(u.db())
	if err != nil || cfg.Accounts[0].Mail.DialIP != "81.19.77.171" {
		t.Fatal(cfg, err)
	}
}
