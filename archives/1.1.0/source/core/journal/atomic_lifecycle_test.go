package journal

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestAtomicLocalWriterLifecycleKeepsUnknownDebtAndUsesTwoCommits(t *testing.T) {
	for _, result := range []Result{Accepted, Uncertain, RejectedBeforeCommit} {
		t.Run(fmt.Sprint(result), func(t *testing.T) {
			s := openTest(t, filepath.Join(t.TempDir(), "data.db"), testConfig(1))
			g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 1})
			commits := 0
			s.beforeCommit = func() error { commits++; return nil }
			a, err := s.PrepareStarted(g.Ref, "itom-u1-up", wire.ID{2}, 1000, []byte("canonical-envelope"))
			if err != nil || a.State != Started {
				t.Fatal(a, err)
			}
			if _, err = s.Start(a.Ref); !errors.Is(err, ErrState) {
				t.Fatal("second writer permission accepted", err)
			}
			// An expected rejected transition rolls back and does not commit.
			if err = s.CompleteAndClose(a.Ref, result); err != nil || commits != 2 {
				t.Fatal("writer lifecycle did not commit atomically", commits, err)
			}
			want := pool.Amount{Bytes: 1000, Messages: 1}
			if result == RejectedBeforeCommit {
				want = pool.Amount{}
			}
			heldTest(t, s, "a0", "12345678", want)
		})
	}
}

func TestAtomicWriterLostStartReplyNeverAuthorizesOrRefunds(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.db")
			s := openTest(t, path, testConfig(1))
			g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 1})
			fault := func() error { return errors.New("lost atomic durable completion") }
			if after {
				s.afterCommit = fault
			} else {
				s.beforeCommit = fault
			}
			permit, err := s.PrepareStarted(g.Ref, "itom-u1-up", wire.ID{2}, 1000, []byte("canonical-envelope"))
			if err == nil || permit.Ref.ID != 0 {
				t.Fatal("unconfirmed durable writer permission leaked", permit, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, path, testConfig(1))
			heldTest(t, s, "a0", "12345678", g.Limit)
			attempts, err := s.ScanAttempts(0, 256)
			if err != nil {
				t.Fatal(err)
			}
			if after {
				if len(attempts) != 1 || attempts[0].State != Unknown {
					t.Fatal("started exposure was forgotten across restart", attempts)
				}
				enabled(t, s, "a0", "12345678")
				if _, err = s.Start(attempts[0].Ref); !errors.Is(err, ErrGeneration) {
					t.Fatal("recovered writer reauthorized", err)
				}
			} else if len(attempts) != 0 {
				t.Fatal("failed transaction retained a partial attempt")
			}
		})
	}
}

func TestAtomicOutcomeAndGrantCloseRecoverTogether(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.db")
			s := openTest(t, path, testConfig(1))
			g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 1})
			a, err := s.PrepareStarted(g.Ref, "itom-u1-up", wire.ID{2}, 1000, []byte("canonical-envelope"))
			if err != nil {
				t.Fatal(err)
			}
			fault := func() error { return errors.New("lost atomic completion") }
			if after {
				s.afterCommit = fault
			} else {
				s.beforeCommit = fault
			}
			if err = s.CompleteAndClose(a.Ref, Uncertain); err == nil {
				t.Fatal("lost completion accepted")
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, path, testConfig(1))
			got, err := s.GetGrant(g.Ref)
			if err != nil || got.Closed != after {
				t.Fatal("outcome and close recovered inconsistently", got, err)
			}
			want := g.Limit
			if after {
				want = pool.Amount{Bytes: 1000, Messages: 1}
			}
			heldTest(t, s, "a0", "12345678", want)
		})
	}
}

func TestStartLocalAttemptScopeRollbackAndExactClosedGrant(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "data.db"), testConfig(1))
	enabled(t, s, "a0", "12345678")
	scope := Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}
	if _, err := s.StartLocalAttempt(scope, "foreign", wire.ID{2}, 1000, []byte("canonical-envelope")); !errors.Is(err, ErrScope) {
		t.Fatal("foreign attempt accepted", err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
	commits := 0
	s.beforeCommit = func() error { commits++; return nil }
	a, err := s.StartLocalAttempt(scope, "itom-u1-up", wire.ID{3}, 1000, []byte("canonical-envelope"))
	if err != nil || a.State != Started {
		t.Fatal(a, err)
	}
	g, err := s.GetGrant(a.Ref.Grant)
	if err != nil || !g.Closed || g.Used != g.Limit || g.Limit != (pool.Amount{Bytes: 1000, Messages: 1}) {
		t.Fatal("one-use exact local grant was not atomically closed", g, err)
	}
	if err = s.Complete(a.Ref, Uncertain); err != nil || commits != 2 {
		t.Fatal("control publication requires more than two durable commits", commits, err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
}

func TestStartLocalAttemptLostReplyKeepsExposureWithoutLeakingPermission(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data.db")
			s := openTest(t, path, testConfig(1))
			enabled(t, s, "a0", "12345678")
			fault := func() error { return errors.New("lost one-transaction start") }
			if after {
				s.afterCommit = fault
			} else {
				s.beforeCommit = fault
			}
			a, err := s.StartLocalAttempt(Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}, "itom-u1-up", wire.ID{2}, 1000, []byte("canonical-envelope"))
			if err == nil || a.Ref.ID != 0 {
				t.Fatal("unconfirmed permission leaked", a, err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, path, testConfig(1))
			attempts, err := s.ScanAttempts(0, 256)
			if err != nil {
				t.Fatal(err)
			}
			if after {
				if len(attempts) != 1 || attempts[0].State != Unknown {
					t.Fatal("started exposure forgotten", attempts)
				}
				heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
			} else {
				if len(attempts) != 0 {
					t.Fatal("rolled-back attempt retained")
				}
				heldTest(t, s, "a0", "12345678", pool.Amount{})
			}
		})
	}
}
