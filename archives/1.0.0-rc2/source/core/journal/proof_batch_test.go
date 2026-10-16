package journal

import (
	"errors"
	"path/filepath"
	"testing"

	"itom.local/core/pool"
)

func proofBatchFixture(t *testing.T) (*Store, string, []ObservedAttempt, []Deletion) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s := openTest(t, path, testConfig(1))
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	var observed []ObservedAttempt
	var deleted []Deletion
	for i := 0; i < 3; i++ {
		a := prepareTest(t, s, g)
		if _, err := s.Start(a.Ref); err != nil {
			t.Fatal(err)
		}
		if err := s.Complete(a.Ref, Uncertain); err != nil {
			t.Fatal(err)
		}
		o := observation()
		o.Location.UID = uint32(i + 1)
		observed = append(observed, ObservedAttempt{Ref: a.Ref, Observation: o})
		deleted = append(deleted, Deletion{Ref: a.Ref, Location: o.Location})
	}
	if err := s.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 3000, Messages: 3})
	return s, path, observed, deleted
}

func TestBatchedProofsShareCommitsAndRollbackInvalidRefund(t *testing.T) {
	s, _, observed, deleted := proofBatchFixture(t)
	commits := 0
	s.beforeCommit = func() error { commits++; return nil }
	if err := s.ObserveMany(s.Run(), observed); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatal("observations did not share one durable commit", commits)
	}
	bad := append([]Deletion(nil), deleted...)
	bad[1].Location.UIDValidity++
	if err := s.DeletedMany(s.Run(), bad); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	duplicate := append([]Deletion(nil), deleted...)
	duplicate[1] = duplicate[0]
	if err := s.DeletedMany(s.Run(), duplicate); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 3000, Messages: 3})
	if commits != 1 {
		t.Fatal("invalid proof reached commit", commits)
	}
	if err := s.DeletedMany(s.Run(), deleted); err != nil {
		t.Fatal(err)
	}
	if commits != 2 {
		t.Fatal("refunds did not share one durable commit", commits)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
}

func TestBatchedObservationRejectsForgedAndAliasedEvidenceAtomically(t *testing.T) {
	s, _, observed, _ := proofBatchFixture(t)
	for _, name := range []string{"forged body", "reused locator", "reused attempt", "stale run", "oversized", "empty"} {
		t.Run(name, func(t *testing.T) {
			proofs := append([]ObservedAttempt(nil), observed...)
			run := s.Run()
			want := ErrScope
			switch name {
			case "forged body":
				proofs[1].Observation.Decoded = []byte("forged-envelope")
			case "reused locator":
				proofs[1].Observation.Location = proofs[0].Observation.Location
			case "reused attempt":
				proofs[1].Ref = proofs[0].Ref
			case "stale run":
				run++
				want = ErrGeneration
			case "oversized":
				proofs = make([]ObservedAttempt, MaxProofBatch+1)
				want = ErrLimit
			case "empty":
				proofs = nil
				want = ErrLimit
			}
			if err := s.ObserveMany(run, proofs); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			for _, proof := range observed {
				a, err := s.GetAttempt(proof.Ref)
				if err != nil || a.State != Unknown || a.Location != (Location{}) {
					t.Fatal("partial observation escaped rollback", a, err)
				}
			}
			heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 3000, Messages: 3})
		})
	}
}

func TestBatchedRefundCommitFailureAndLostAcknowledgementRecoverAtomically(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "before commit"
		if lost {
			name = "lost acknowledgement"
		}
		t.Run(name, func(t *testing.T) {
			s, path, observed, deleted := proofBatchFixture(t)
			if err := s.ObserveMany(s.Run(), observed); err != nil {
				t.Fatal(err)
			}
			fault := errors.New("injected commit failure")
			if lost {
				s.afterCommit = func() error { return fault }
			} else {
				s.beforeCommit = func() error { return fault }
			}
			if err := s.DeletedMany(s.Run(), deleted); !errors.Is(err, fault) {
				t.Fatal(err)
			}
			if _, err := s.Held("a0", "12345678"); !errors.Is(err, ErrPoisoned) {
				t.Fatal("failed commit remained usable", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := openTest(t, path, testConfig(1))
			want := pool.Amount{Bytes: 3000, Messages: 3}
			if lost {
				want = pool.Amount{}
			}
			heldTest(t, reopened, "a0", "12345678", want)
		})
	}
}
