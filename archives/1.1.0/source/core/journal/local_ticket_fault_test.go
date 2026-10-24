package journal

import (
	"errors"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalTicketIOFailuresNeverReturnWriterPermissionAndRetainCommittedDebt(t *testing.T) {
	for _, point := range []string{"reserve_before", "reserve_after", "body_before", "body_after"} {
		t.Run(point, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "issuer.db")
			cfg := Config{Accounts: []Account{{ID: "account1", Provider: "fixture", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Users: []User{{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 20, MaxAttempts: 20}
			store, err := Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.EnableAfterInventory("account1", "12345678", store.Run()); err != nil {
				t.Fatal(err)
			}
			fault := func() error { return errors.New("injected IO ambiguity") }
			install := func() {
				if point == "reserve_before" || point == "body_before" {
					store.beforeCommit = fault
				} else {
					store.afterCommit = fault
				}
			}
			if point == "reserve_before" || point == "reserve_after" {
				install()
			}
			scope := Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
			ticket, _, err := store.ReserveLocalSized(scope, "bulk", 1, 100, func(n int) (uint64, error) { return uint64(n + 1000), nil })
			if point == "reserve_before" || point == "reserve_after" {
				if err == nil || ticket != nil {
					t.Fatal("ambiguous reservation returned admission")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				install()
				if attempt, err := ticket.Commit(wire.ID{2}, 1050, []byte("owned body")); err == nil || attempt.Ref.ID != 0 {
					t.Fatal("ambiguous body commit returned writer permission", attempt, err)
				}
				if err = ticket.Cancel(); !errors.Is(err, ErrPoisoned) {
					t.Fatal("poisoned ticket released speculative allocation", err)
				}
			}
			store.Close()
			store, err = Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			expected := pool.Amount{Bytes: 1100, Messages: 1}
			if point == "reserve_before" {
				expected = pool.Amount{}
			} else if point == "body_after" {
				expected.Bytes = 1050
			}
			if held, err := store.Held(scope.Account, scope.User); err != nil || held != expected {
				t.Fatal("restart changed conservative physical exposure", held, expected, err)
			}
			attempts, err := store.ScanAttempts(0, 20)
			if err != nil {
				t.Fatal(err)
			}
			if point == "body_after" {
				if len(attempts) != 1 || attempts[0].State != Unknown || attempts[0].EncodedBytes != 1050 {
					t.Fatal("atomic body intent missing after lost commit ACK", attempts)
				}
				grant, err := store.GetGrant(attempts[0].Ref.Grant)
				if err != nil || !grant.Closed || grant.Used.Bytes != 1050 {
					t.Fatal("body and grant close were partially committed", err)
				}
			} else if len(attempts) != 0 {
				t.Fatal("uncommitted body created a writer intent", attempts)
			}
			if err = store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
