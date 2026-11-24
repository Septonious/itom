package journal

import (
	"bytes"
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

// These are recovery-only fault cases. They do not inject an imaginary outage
// or disk cost into the primary healthy-provider performance profile.
func TestAtomicBodyReceiptSurvivesLostCommitAcknowledgmentAndRollsBackPrecommitFailure(t *testing.T) {
	for _, after := range []bool{false, true} {
		name := "before_commit"
		if after {
			name = "after_commit_lost_ack"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.db")
			cfg := Config{Accounts: []Account{{ID: "account1", Provider: "fixture", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Users: []User{{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 32, MaxAttempts: 32}
			store, e := Open(path, cfg, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if e = store.EnableAfterInventory("account1", "12345678", store.Run()); e != nil {
				t.Fatal(e)
			}
			grant, e := store.IssueRemote(Scope{Account: "account1", User: "12345678", Session: wire.ID{7}}, pool.Amount{Bytes: 8000, Messages: 8})
			if e != nil {
				t.Fatal(e)
			}
			offer, e := store.RemoteOffer(grant.Ref)
			if e != nil {
				t.Fatal(e)
			}
			report, body, e := wire.BindDataEnvelope(offer.Key, 1, wire.ID{8}, 0, "fast", []byte("fixture packet"), bytes.Repeat([]byte{9}, 32), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
			if e != nil {
				t.Fatal(e)
			}
			proof := Observation{Account: "account1", Folder: "fast", EncodedBytes: report.Attempt.EncodedBytes, Location: Location{UIDValidity: 5, UID: 17}, Decoded: body}
			fault := func() error { return errors.New("injected lost commit acknowledgment") }
			if after {
				store.afterCommit = fault
			} else {
				store.beforeCommit = fault
			}
			if _, _, e = store.AcceptRemoteBody("12345678", store.Run(), report, proof); e == nil {
				t.Fatal("injected IO ambiguity returned success")
			}
			store.Close()
			store, e = Open(path, cfg, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			defer store.Close()
			actual, e := store.GetGrant(grant.Ref)
			if e != nil {
				t.Fatal(e)
			}
			if after {
				if actual.Registered != pool.Amount(report.Total) || actual.Used != pool.Amount(report.Total) {
					t.Fatal("committed accounting disappeared after lost ACK", actual)
				}
				ref, e := store.RemoteAttempt(report.Key, 1)
				if e != nil {
					t.Fatal(e)
				}
				attempt, e := store.GetAttempt(ref)
				if e != nil || attempt.State != Committed || attempt.Location != proof.Location {
					t.Fatal("receipt was only partially committed", attempt, e)
				}
			} else {
				if actual.Registered != (pool.Amount{}) || actual.Used != (pool.Amount{}) {
					t.Fatal("precommit failure left partial accounting", actual)
				}
				if _, e = store.RemoteAttempt(report.Key, 1); !errors.Is(e, ErrMissing) {
					t.Fatal("precommit failure left an attempt row", e)
				}
			}
			held, e := store.Held("account1", "12345678")
			if e != nil || held != grant.Limit {
				t.Fatal("failed acknowledgment refunded reserved quota", held, e)
			}
			if _, _, e = store.AcceptRemoteBody("12345678", store.Run(), report, proof); e != nil {
				t.Fatal("positive scoped inventory could not reconcile exact body", e)
			}
			current, e := store.GetGrant(grant.Ref)
			if e != nil || current.Registered.Messages != 1 || current.Released.Messages != 0 {
				t.Fatal("reconciliation duplicated or refunded body", current, e)
			}
		})
	}
}
