package tunnel

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestInstalledIssuerProfilesMigrateToIsolatedFoldersWithoutRaisingSavedLimits(t *testing.T) {
	a := servercfg.Account{ID: "account1", Mail: mailbox.Config{Host: "fixture.local"}}
	u := servercfg.User{ID: "12345678", Key: strings.Repeat("17", 32), Up: "itom-up", Down: "itom-down"}
	for _, size := range []uint64{8, 12, 16} {
		for _, shards := range []int{0, 1, 2, 4} {
			t.Run(fmt.Sprintf("data%d_shards%d", size, shards), func(t *testing.T) {
				root := t.TempDir()
				limit := pool.Amount{Bytes: size << 20, Messages: map[uint64]uint64{8: 128, 12: 192, 16: 256}[size]}
				old := diskConfig(a, u, limit, shards)
				s, err := journal.Open(filepath.Join(root, "data.db"), old, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				id := s.StoreID()
				if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
					t.Fatal(err)
				}
				g, err := s.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{9}}, pool.Amount{Bytes: 65536, Messages: 1})
				if err != nil {
					t.Fatal(err)
				}
				attempt, err := s.PrepareStarted(g.Ref, u.Down, wire.ID{8}, 65536, []byte("old unknown ciphertext"))
				if err != nil {
					t.Fatal(err)
				}
				if err = s.CompleteAndClose(attempt.Ref, journal.Uncertain); err != nil {
					t.Fatal(err)
				}
				s.Close()
				for round := 0; round < 2; round++ {
					s, err = openIssuer(root, "data", a, u)
					if err != nil {
						t.Fatal("installed profile refused", err)
					}
					got, err := s.GetAttempt(attempt.Ref)
					if err != nil || got.State != journal.Unknown || s.StoreID() != id {
						t.Fatal("original identity/debt lost", got, err)
					}
					actual, _ := s.Limit(a.ID, u.ID)
					held, _ := s.Held(a.ID, u.ID)
					if actual != limit || held != (pool.Amount{Bytes: 65536, Messages: 1}) || len(s.Config().Accounts[0].Users[0].Folders) != len(old.Accounts[0].Users[0].Folders)+2 {
						t.Fatal("migration changed limits/debt/folder coverage", actual, held, s.Config())
					}
					s.Close()
				}
			})
		}
	}
}
