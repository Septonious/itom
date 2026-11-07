package manager

import (
	"context"
	"errors"
	"io"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"path/filepath"
	"testing"
)

func TestServiceLifecycleCannotRefundUncollectedMailboxDebt(t *testing.T) {
	for _, operation := range []string{"first_start", "stop", "restart", "update", "failed_purge"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			path := seedCapacity(t, filepath.Join(dir, "tunnel-server"), "m1-owned", "s2c", 67_000_000)
			if operation == "failed_purge" {
				ui := New(dir)
				ui.Out = io.Discard
				ui.stoppedCheck = func() error { return nil }
				ui.adminDial = func(context.Context, mailbox.Config) (accountAdmin, error) {
					return nil, errors.New("offline provider")
				}
				if err := ui.purgeAllAccounts(servercfg.Config{Accounts: []servercfg.Account{{ID: "a01", Mail: mailbox.Config{Username: "fixture"}}}}); err == nil {
					t.Fatal("purge failure hidden")
				}
			} else {
				for range 3 {
					if err := VerifyCapacityState(dir); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := capacityValue(t, path); got != 67_000_000 {
				t.Fatalf("%s forgave uncollected mailbox debt: got%d want67000000", operation, got)
			}
		})
	}
}
