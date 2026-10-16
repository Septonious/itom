package manager

import (
	"errors"
	"itom.local/core/servercfg"
	"os"
	"path/filepath"
	"testing"
)

func TestRev6ResetAccountsRequiresConfirmationAndStoppedService(t *testing.T) {
	for _, tc := range []struct {
		input   string
		running bool
		cleared bool
	}{{"6\nno\n", false, false}, {"6\nRESET ACCOUNTS\n", true, false}, {"6\nRESET ACCOUNTS\n", false, true}} {
		u, _ := accountUI(t, tc.input)
		path := filepath.Join(u.State, "old-journal.db")
		os.WriteFile(path, []byte("physical debt"), 0600)
		if tc.running {
			u.stoppedCheck = func() error { return errors.New("service active") }
		}
		err := u.accounts()
		if tc.running && err == nil {
			t.Fatal("running service allowed reset")
		}
		if !tc.running && err != nil {
			t.Fatal(err)
		}
		c, err := servercfg.Load(u.db())
		if err != nil || (len(c.Accounts) == 0) != tc.cleared {
			t.Fatal("wrong reset result", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil || string(raw) != "physical debt" {
			t.Fatal("reset changed journal")
		}
	}
}
