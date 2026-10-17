package journal

import (
	"errors"
	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"path/filepath"
	"testing"
)

func TestRemoteIndexExactOwnershipRestartAndDeletion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	cfg := testConfig(1)
	s := openTest(t, path, cfg)
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 4000, Messages: 4})
	report, body := descriptor(t, g, 1, 0)
	applyTest(t, s, report)
	ref, err := s.RemoteAttempt(report.Key, 1)
	if err != nil || ref != attemptOrdinal(t, s, 1).Ref {
		t.Fatal(ref, err)
	}
	wrong := report.Key
	wrong.User = "87654321"
	if _, err = s.RemoteAttempt(wrong, 1); !errors.Is(err, ErrScope) {
		t.Fatal("foreign user", err)
	}
	wrong = report.Key
	wrong.Epoch++
	if _, err = s.RemoteAttempt(wrong, 1); !errors.Is(err, ErrScope) {
		t.Fatal("foreign run", err)
	}
	if _, err = s.RemoteAttempt(report.Key, 2); !errors.Is(err, ErrMissing) {
		t.Fatal(err)
	}
	// Simulate a legacy process which does not maintain the derived bucket.
	if err = s.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(remoteIndexBucket) }); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openTest(t, path, cfg)
	defer s.Close()
	if _, err = s.RemoteAttempt(report.Key, 1); err != nil {
		t.Fatal("index reconstruction", err)
	}
	cleanRemote(t, s, attemptOrdinal(t, s, 1), body)
	if _, err = s.RemoteAttempt(report.Key, 1); !errors.Is(err, ErrMissing) {
		t.Fatal("deleted row returned", err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
