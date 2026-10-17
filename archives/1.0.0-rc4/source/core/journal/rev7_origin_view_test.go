package journal

import (
	"errors"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func rev7CompareOriginToRows(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.ScanGrants(0, 256)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"a0", "a1"} {
		for _, user := range []string{"12345678", "87654321"} {
			var wantLocal, wantRemote pool.Amount
			for _, g := range rows {
				if g.Ref.Scope.Account == account && g.Ref.Scope.User == user {
					if g.Remote {
						wantRemote = add(wantRemote, held(g))
					} else {
						wantLocal = add(wantLocal, held(g))
					}
				}
			}
			for _, session := range []wire.ID{{1}, {7}} {
				var oldLocal, oldRemote pool.Amount
				for _, g := range rows {
					if g.Ref.Scope.Account == account && g.Ref.Scope.User == user && g.Ref.Scope.Session != session {
						if g.Remote {
							oldRemote = add(oldRemote, held(g))
						} else {
							oldLocal = add(oldLocal, held(g))
						}
					}
				}
				view, e := s.UsageByOrigin(account, user, session)
				if e != nil || view.Local != wantLocal || view.Remote != wantRemote || view.HistoricalLocal != oldLocal || view.HistoricalRemote != oldRemote {
					t.Fatal("snapshot/session view differs from actual rows", view, e)
				}
			}
			for i := 0; i < 3; i++ {
				local, remote, err := s.HeldByOrigin(account, user)
				if err != nil || local != wantLocal || remote != wantRemote {
					t.Fatal("origin differs from committed rows", account, user, local, remote, wantLocal, wantRemote, err)
				}
			}
		}
	}
}

func TestRev7OriginViewTracksUnknownDebtFencesProofsAndRollback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "origin.db")
	s := openTest(t, p, testConfig(2))
	local := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 2000, Messages: 2})
	remote := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 6000, Messages: 6})
	issueTest(t, s, "a1", "87654321", pool.Amount{Bytes: 1500, Messages: 2})
	rev7CompareOriginToRows(t, s)
	a := prepareTest(t, s, local)
	s.Start(a.Ref)
	s.Complete(a.Ref, Uncertain)
	s.CloseGrant(local.Ref)
	rev7CompareOriginToRows(t, s)
	report, body := descriptor(t, remote, 1, 0)
	applyTest(t, s, report)
	applyTest(t, s, wire.StorageRelease{Key: quotaKey(remote), Final: report.Total})
	rev7CompareOriginToRows(t, s)
	bad := observation()
	bad.Location.UIDValidity = 0
	if err := s.Observe(a.Ref, s.Run(), bad); err == nil {
		t.Fatal("invalid proof accepted")
	}
	rev7CompareOriginToRows(t, s)
	cleanRemote(t, s, attemptOrdinal(t, s, 1), body)
	rev7CompareOriginToRows(t, s)
	if err := s.Observe(a.Ref, s.Run(), observation()); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted(a.Ref, s.Run(), observation().Location); err != nil {
		t.Fatal(err)
	}
	rev7CompareOriginToRows(t, s)
	s.beforeCommit = func() error { return ErrLimit }
	_, err := s.Issue(Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 500, Messages: 1})
	if !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	s.beforeCommit = nil
	rev7CompareOriginToRows(t, s)
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, p, testConfig(2))
	rev7CompareOriginToRows(t, s)
}

func TestRev7OriginViewNeverHidesLostCommitPoisoning(t *testing.T) {
	p := filepath.Join(t.TempDir(), "poison.db")
	s := openTest(t, p, testConfig(2))
	issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
	rev7CompareOriginToRows(t, s)
	s.afterCommit = func() error { return errors.New("lost durable commit acknowledgement") }
	if _, err := s.Issue(Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 500, Messages: 1}); err == nil {
		t.Fatal("lost reply swallowed")
	}
	if _, _, err := s.HeldByOrigin("a0", "12345678"); !errors.Is(err, ErrPoisoned) {
		t.Fatal("cached success bypassed poisoned store", err)
	}
	s.Close()
	s = openTest(t, p, testConfig(2))
	rev7CompareOriginToRows(t, s)
	local, remote, err := s.HeldByOrigin("a0", "12345678")
	if err != nil || local.Bytes != 1500 || remote.Bytes != 0 {
		t.Fatal("persisted uncertain outcome forgotten", local, remote, err)
	}
}
