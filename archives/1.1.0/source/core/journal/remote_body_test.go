package journal_test

import (
	"bytes"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
)

func remoteBody(t *testing.T, s *journal.Store) (journal.Grant, wire.UsageReport, journal.Observation) {
	t.Helper()
	g, e := s.IssueRemote(journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{7}}, pool.Amount{Bytes: 8000, Messages: 8})
	if e != nil {
		t.Fatal(e)
	}
	offer, e := s.RemoteOffer(g.Ref)
	if e != nil {
		t.Fatal(e)
	}
	report, body, e := wire.BindDataEnvelope(offer.Key, 1, wire.ID{8}, 0, "bulk", []byte("authenticated encrypted packet"), bytes.Repeat([]byte{9}, 32), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if e != nil {
		t.Fatal(e)
	}
	proof := journal.Observation{Account: "account1", Folder: "bulk", EncodedBytes: report.Attempt.EncodedBytes, Location: journal.Location{UIDValidity: 5, UID: 17}, Decoded: body}
	return g, report, proof
}
func TestReceivedBodyRegistrationAndObservationHaveOneDurableCommit(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "journal.db"))
	defer s.Close()
	enable(t, s)
	g, report, proof := remoteBody(t, s)
	before := s.CommitSnapshot()
	status, ref, e := s.AcceptRemoteBody("12345678", s.Run(), report, proof)
	if e != nil {
		t.Fatal(e)
	}
	if s.CommitSnapshot().Count-before.Count != 1 {
		t.Fatal("receive path added a second commit")
	}
	actual, e := s.GetAttempt(ref)
	if e != nil || actual.State != journal.Committed || actual.Location != proof.Location {
		t.Fatal(actual, e)
	}
	if status.Registered != report.Total || status.Released != (wire.Quota{}) {
		t.Fatal("receive acknowledgment invented a refund", status)
	}
	if current, e := s.GetGrant(g.Ref); e != nil || current.Used.Bytes != report.Total.Bytes {
		t.Fatal(current, e)
	}
}
func TestBadStoredBodyProofRollsBackRegistrationAndCannotPoisonLaterDelivery(t *testing.T) {
	for _, name := range []string{"body", "folder", "account", "size", "uid"} {
		t.Run(name, func(t *testing.T) {
			s := open(t, filepath.Join(t.TempDir(), "journal.db"))
			defer s.Close()
			enable(t, s)
			g, report, proof := remoteBody(t, s)
			bad := proof
			switch name {
			case "body":
				bad.Decoded = append([]byte(nil), proof.Decoded...)
				bad.Decoded[len(bad.Decoded)-1] ^= 1
			case "folder":
				bad.Folder = "fast"
			case "account":
				bad.Account = "other"
			case "size":
				bad.EncodedBytes++
			case "uid":
				bad.Location.UID = 0
			}
			if _, _, e := s.AcceptRemoteBody("12345678", s.Run(), report, bad); e == nil {
				t.Fatal("invalid proof was accepted")
			}
			current, e := s.GetGrant(g.Ref)
			if e != nil || current.Registered.Messages != 0 || current.Used.Messages != 0 {
				t.Fatal("partial registration survived failed proof", current, e)
			}
			if _, e = s.RemoteAttempt(report.Key, 1); !errors.Is(e, journal.ErrMissing) {
				t.Fatal("failed proof left an attempt row", e)
			}
			if _, _, e = s.AcceptRemoteBody("12345678", s.Run(), report, proof); e != nil {
				t.Fatal("failed proof poisoned a later correct body", e)
			}
		})
	}
}
