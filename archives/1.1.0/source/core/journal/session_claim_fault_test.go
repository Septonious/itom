package journal

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestInitialInvitationCommitFailureNeverReturnsPermissionAndReopenKeepsDebt(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_commit", true: "lost_commit_ack"}[after], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.db")
			cfg := Config{Accounts: []Account{{ID: "account1", Provider: "fixture", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Users: []User{{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 20, MaxAttempts: 20}
			s, err := Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory("account1", "12345678", s.Run()); err != nil {
				t.Fatal(err)
			}
			fault := func() error { return errors.New("injected lost persistence acknowledgement") }
			if after {
				s.afterCommit = fault
			} else {
				s.beforeCommit = fault
			}
			scope := Scope{Account: "account1", User: "12345678", Session: wire.ID{61}}
			allowance := pool.Amount{Bytes: 3000, Messages: 3}
			if grant, err := s.IssueInitialRemote(scope, allowance); err == nil || grant.Ref.ID != 0 {
				t.Fatal("uncertain issue returned a publishable offer", grant, err)
			}
			if _, err = s.IssueInitialRemote(scope, allowance); !errors.Is(err, ErrPoisoned) {
				t.Fatal("failed issuer allowed another initial allocation", err)
			}
			s.Close()
			s, err = Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			expected := pool.Amount{}
			if after {
				expected = allowance
			}
			if held, err := s.Held(scope.Account, scope.User); err != nil || held != expected {
				t.Fatal("reopen inferred zero debt from an absent invitation", held, expected, err)
			}
			grants, err := s.ScanGrants(0, 20)
			if err != nil {
				t.Fatal(err)
			}
			if after {
				if len(grants) != 1 {
					t.Fatal("committed original allocation lost", grants)
				}
				if _, err = s.RemoteOffer(grants[0].Ref); !errors.Is(err, ErrGeneration) {
					t.Fatal("reopen reauthorized an old offer", err)
				}
				if err = s.ClaimUnusedRemoteSession(grants[0].Ref); !errors.Is(err, ErrGeneration) {
					t.Fatal("old-run invitation regained startup ownership", err)
				}
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
