package journal

import (
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/pool"
	"itom.local/core/wire"
)

// Duplicate evidence remains fully authenticated but must not occupy the
// durable writer or invoke a commit hook when no byte of state changes.
func TestRC11DuplicateProofSkipsCommitWithoutSkippingValidation(t *testing.T) {
	s, _, proofs, _ := proofBatchFixture(t)
	if err := s.ObserveMany(s.Run(), proofs); err != nil {
		t.Fatal(err)
	}
	before := s.CommitSnapshot()
	s.beforeCommit = func() error { t.Fatal("duplicate proof reached fsync"); return nil }
	s.afterCommit = func() error { t.Fatal("duplicate proof fabricated commit"); return nil }
	for i := 0; i < 100; i++ {
		if err := s.ObserveMany(s.Run(), proofs); err != nil {
			t.Fatal(err)
		}
	}
	after := s.CommitSnapshot()
	if after.Count != before.Count || after.Noop-before.Noop != 100 {
		t.Fatal(before, after)
	}
	bad := append([]ObservedAttempt(nil), proofs...)
	bad[1].Observation.Decoded = []byte("forged-envelope")
	if err := s.ObserveMany(s.Run(), bad); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 3000, Messages: 3})
}

func TestRC11MixedProofStillCommitsAndRollsBackAtomically(t *testing.T) {
	s, _, proofs, _ := proofBatchFixture(t)
	if err := s.ObserveMany(s.Run(), proofs[:1]); err != nil {
		t.Fatal(err)
	}
	bad := append([]ObservedAttempt(nil), proofs...)
	bad[2].Observation.Decoded = []byte("forged-envelope")
	if err := s.ObserveMany(s.Run(), bad); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	a, err := s.GetAttempt(proofs[1].Ref)
	if err != nil || a.State != Unknown {
		t.Fatal("mixed batch leaked state", a, err)
	}
	commits := 0
	s.beforeCommit = func() error { commits++; return nil }
	if err := s.ObserveMany(s.Run(), proofs); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatal("mixed changed batch did not commit", commits)
	}
}

func TestRC11RemoteDuplicateStillChecksAuthorityAndKeepsUnknownDebt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTest(t, path, testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	report, _ := descriptor(t, g, 1, 0)
	want := applyTest(t, s, report)
	before := s.CommitSnapshot()
	s.beforeCommit = func() error { t.Fatal("duplicate report reached fsync"); return nil }
	for i := 0; i < 100; i++ {
		if got := applyTest(t, s, report); got != want {
			t.Fatal(got, want)
		}
	}
	if got := s.CommitSnapshot(); got.Count != before.Count || got.Noop-before.Noop != 100 {
		t.Fatal(got, before)
	}
	if _, err := s.ApplyRemote("otheruser", s.Run(), report); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	if _, err := s.ApplyRemote("12345678", s.Run()+1, report); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	forged := report
	forged.Attempt = new(wire.PhysicalDescriptor)
	*forged.Attempt = *report.Attempt
	forged.Attempt.Folder = "foreign"
	if _, err := s.ApplyRemote("12345678", s.Run(), forged); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	s.beforeCommit = nil
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openTest(t, path, testConfig(1))
	heldTest(t, reopened, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a, err := reopened.GetAttempt(attemptOrdinal(t, reopened, 1).Ref)
	if err != nil || a.Location != (Location{}) {
		t.Fatal("duplicate manufactured observation", a, err)
	}
}

func TestRC11ChangedRemoteReportStillPoisonsAfterLostCommit(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	report, _ := descriptor(t, g, 1, 0)
	fault := errors.New("lost commit reply")
	s.afterCommit = func() error { return fault }
	if _, err := s.ApplyRemote("12345678", s.Run(), report); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	if _, err := s.ApplyRemote("12345678", s.Run(), report); !errors.Is(err, ErrPoisoned) {
		t.Fatal("lost commit bypassed poison", err)
	}
}
