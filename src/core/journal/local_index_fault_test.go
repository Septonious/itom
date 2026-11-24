package journal

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestLocalCorrelationHasNoSpeculativeEntryAfterLostCommitAndRebuildsMissingMetadata(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before_commit"
		if after {
			name = "lost_commit_ack"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "issuer.db")
			cfg := Config{Accounts: []Account{{ID: "account1", Provider: "fixture", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Users: []User{{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 20, MaxAttempts: 20}
			s, err := Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory("account1", "12345678", s.Run()); err != nil {
				t.Fatal(err)
			}
			ticket, _, err := s.ReserveLocalSized(Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}, "fast", 1, 100, func(n int) (uint64, error) { return uint64(n + 1000), nil })
			if err != nil {
				t.Fatal(err)
			}
			k := ticket.Key()
			fault := func() error { return errors.New("injected metadata commit ambiguity") }
			if after {
				s.afterCommit = fault
			} else {
				s.beforeCommit = fault
			}
			if a, err := ticket.Commit(wire.ID{2}, 1050, []byte("owned body")); err == nil || a.Ref.ID != 0 {
				t.Fatal("ambiguous commit granted writer permission", a, err)
			}
			s.Close()
			s, err = Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ref, err := s.LocalAttempt(k, wire.ID{2})
			if !after {
				defer s.Close()
				if !errors.Is(err, ErrMissing) || ref.ID != 0 {
					t.Fatal("failed commit created speculative correlation", ref, err)
				}
				return
			}
			if err != nil || ref.ID == 0 {
				t.Fatal("durable body intent lost with commit reply", ref, err)
			}
			a, err := s.GetAttempt(ref)
			if err != nil || a.State != Unknown {
				t.Fatal("recovered attempt became safe to re-publish", a, err)
			}
			// Emulate a compatible older writer which never maintained the derived
			// metadata. Original attempt/grant records are left completely intact.
			if err = s.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket(localIndexBucket) }); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if recovered, err := s.LocalAttempt(k, wire.ID{2}); err != nil || recovered != ref {
				t.Fatal("missing derived metadata not rebuilt", recovered, err)
			}
			held, err := s.Held("account1", "12345678")
			if err != nil || held.Bytes != 1050 || held.Messages != 1 {
				t.Fatal("index rebuild changed debt", held, err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
