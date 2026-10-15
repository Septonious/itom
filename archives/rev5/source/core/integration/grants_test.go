package integration

import (
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wire"
)

func grantThroughNoise(t *testing.T, tx, rx *secure.Epoch, m wire.Control) wire.Control {
	t.Helper()
	f, e := wire.EncodeControl(0, m)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := wire.DecodeControl(throughNoise(t, tx, rx, f))
	if e != nil {
		t.Fatal(e)
	}
	return decoded
}

// Real Noise/control/journal path with reordered RELEASE, missing descriptors,
// lost GC acknowledgements and a coordinator restart. IMAP completions here
// are injected trusted scanner results, not a network server test.
func TestNoiseRemoteGrantRecoveryAndExactlyOnceCleanup(t *testing.T) {
	left, right := epochs(t)
	c := journal.Config{MaxGrants: 8, MaxAttempts: 16, Accounts: []journal.Account{
		{ID: "mail", Provider: "imap", Limit: pool.Amount{Bytes: 20000, Messages: 20}, Users: []journal.User{
			{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"u1-up", "u1-down"}},
			{ID: "87654321", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"u2-up", "u2-down"}},
		}},
	}}
	path := filepath.Join(t.TempDir(), "journal.db")
	s, e := journal.Open(path, c, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	})
	if e = s.EnableAfterInventory("mail", "12345678", s.Run()); e != nil {
		t.Fatal(e)
	}
	g, e := s.IssueRemote(journal.Scope{Account: "mail", User: "12345678", Session: wire.ID{7}}, pool.Amount{Bytes: 10000, Messages: 10})
	if e != nil {
		t.Fatal(e)
	}
	offer, e := s.RemoteOffer(g.Ref)
	if e != nil {
		t.Fatal(e)
	}
	first := grantThroughNoise(t, right, left, offer).(wire.StorageGrant)
	second := grantThroughNoise(t, right, left, offer).(wire.StorageGrant)
	if first != second {
		t.Fatal("duplicate grant changed identity")
	}
	// This wire/ledger fixture constructs two distinct physical
	// attempts explicitly, with their original identity and cumulative spend.
	var reports []wire.UsageReport
	var bodies [][]byte
	for n := uint64(1); n <= 2; n++ {
		batch := wire.ID{byte(n)}
		body, err := wire.BindPhysical(offer.Key, n, batch, []byte("opaque Noise record"))
		if err != nil {
			t.Fatal(err)
		}
		d := wire.PhysicalDescriptor{Batch: batch, EncodedBytes: 1000, DecodedBytes: uint64(len(body)), Digest: sha256.Sum256(body), Folder: "u1-up"}
		reports = append(reports, wire.UsageReport{Key: offer.Key, Total: wire.Quota{Bytes: 1000 * n, Messages: n}, Attempt: &d})
		bodies = append(bodies, body)
	}
	release := wire.StorageRelease{Key: offer.Key, Final: wire.Quota{Bytes: 2000, Messages: 2}}
	// RELEASE overtakes usage. The two used slots remain physically charged.
	if _, e = s.ApplyRemote("12345678", s.Run(), grantThroughNoise(t, left, right, release)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyRemote("12345678", s.Run(), grantThroughNoise(t, left, right, reports[1])); e != journal.ErrOrder {
		t.Fatal(e)
	}
	for _, r := range reports {
		if _, e = s.ApplyRemote("12345678", s.Run(), grantThroughNoise(t, left, right, r)); e != nil {
			t.Fatal(e)
		}
	}
	rows, e := s.ScanAttempts(0, 256)
	if e != nil || len(rows) != 2 {
		t.Fatal(rows, e)
	}
	a := rows[0]
	loc := journal.Location{UIDValidity: 123, UID: 41}
	if e = s.Observe(a.Ref, s.Run(), journal.Observation{EncodedBytes: 1000, Account: "mail", Folder: "u1-up", Location: loc, Decoded: bodies[0]}); e != nil {
		t.Fatal(e)
	}
	if e = s.Deleted(a.Ref, s.Run(), loc); e != nil {
		t.Fatal(e)
	}
	// GC status is lost. Restart and establish fresh Noise keys. The new
	// session may reconcile the old grant for this authenticated user only.
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = journal.Open(path, c, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	left, right = epochs(t)
	status, e := s.ApplyRemote("12345678", s.Run(), grantThroughNoise(t, left, right, reports[0]))
	if e != nil {
		t.Fatal(e)
	}
	got := grantThroughNoise(t, right, left, status).(wire.CleanupStatus)
	if got.Registered.Messages != 2 || got.Released.Messages != 1 || !got.Closed {
		t.Fatal(got)
	}
	rows, e = s.ScanAttempts(0, 256)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	a = rows[0]
	loc.UID = 42
	if e = s.Observe(a.Ref, s.Run(), journal.Observation{EncodedBytes: 1000, Account: "mail", Folder: "u1-up", Location: loc, Decoded: bodies[1]}); e != nil {
		t.Fatal(e)
	}
	if e = s.Deleted(a.Ref, s.Run(), loc); e != nil {
		t.Fatal(e)
	}
	if status, e := s.ApplyRemote("12345678", s.Run(), grantThroughNoise(t, left, right, release)); e != nil || !status.Closed || status.Used != status.Released {
		t.Fatal(e)
	}
	held, e := s.Held("mail", "")
	if e != nil || held != (pool.Amount{}) {
		t.Fatal(held, e)
	}
	if e = s.Audit(); e != nil {
		t.Fatal(e)
	}
}
