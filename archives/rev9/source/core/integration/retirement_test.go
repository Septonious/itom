package integration

import (
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"path/filepath"
	"testing"
	"time"
)

func TestFinalGCAndRetirementSurviveLostRepliesAndBothRestarts(t *testing.T) {
	cfg := journal.Config{MaxGrants: 4, MaxAttempts: 8, Accounts: []journal.Account{{ID: "mail", Provider: "test", Limit: pool.Amount{Bytes: 20000, Messages: 20}, Users: []journal.User{{ID: "12345678", Limit: pool.Amount{Bytes: 20000, Messages: 20}, Folders: []string{"up", "down"}}}}}}
	dir := t.TempDir()
	sp, wp := filepath.Join(dir, "server.db"), filepath.Join(dir, "wallet.db")
	s, e := journal.Open(sp, cfg, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	if e = s.EnableAfterInventory("mail", "12345678", s.Run()); e != nil {
		t.Fatal(e)
	}
	w, e := wallet.Open(wp, "12345678", s.StoreID())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { w.Close() }()
	g, e := s.IssueRemote(journal.Scope{Account: "mail", User: "12345678", Session: w.Session()}, pool.Amount{Bytes: 10000, Messages: 10})
	if e != nil {
		t.Fatal(e)
	}
	offer, e := s.RemoteOffer(g.Ref)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Accept(offer); e != nil {
		t.Fatal(e)
	}
	usage, body, e := w.Spend(offer.Key, "up", []byte("ciphertext"), func([]byte) (uint64, error) { return 1000, nil })
	if e != nil {
		t.Fatal(e)
	}
	left, right := epochs(t)
	status, e := s.ApplyRemote("12345678", s.Run(), grantThroughNoise(t, left, right, usage))
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Acknowledge(grantThroughNoise(t, right, left, status).(wire.CleanupStatus)); e != nil {
		t.Fatal(e)
	}
	release, e := w.Fence(offer.Key)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ApplyRemote("12345678", s.Run(), release)
	if e != nil {
		t.Fatal(e)
	}
	r := wire.Retirement{Key: offer.Key, Final: usage.Total}
	if _, e = s.RetireRemote("12345678", s.Run(), r); e == nil {
		t.Fatal("retired before physical cleanup")
	}
	rows, e := s.ScanAttempts(0, 8)
	if e != nil || len(rows) != 1 {
		t.Fatal(e)
	}
	loc := journal.Location{UIDValidity: 1, UID: 1}
	if e = s.Observe(rows[0].Ref, s.Run(), journal.Observation{Account: "mail", Folder: "up", EncodedBytes: 1000, Location: loc, Decoded: body}); e != nil {
		t.Fatal(e)
	}
	if e = s.Deleted(rows[0].Ref, s.Run(), loc); e != nil {
		t.Fatal(e)
	}
	// Final cleanup response is lost. Neither process gets to forget the key.
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = journal.Open(sp, cfg, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	w, e = wallet.Open(wp, "12345678", s.StoreID())
	if e != nil {
		t.Fatal(e)
	}
	status, e = s.ApplyRemote("12345678", s.Run(), release)
	if e != nil || !status.Closed || status.Released != usage.Total {
		t.Fatal(status, e)
	}
	if e = w.Acknowledge(status); e != nil {
		t.Fatal(e)
	}
	out, e := w.Retirements()
	if e != nil || len(out) != 1 {
		t.Fatal(out, e)
	}
	if _, e = s.RetireRemote("87654321", s.Run(), out[0]); e == nil {
		t.Fatal("wrong user")
	}
	// Server commits metadata removal, but its acknowledgement is lost too.
	if _, e = s.RetireRemote("12345678", s.Run(), out[0]); e != nil {
		t.Fatal(e)
	}
	s.Close()
	w.Close()
	s, e = journal.Open(sp, cfg, time.Second)
	if e != nil {
		t.Fatal(e)
	}
	w, e = wallet.Open(wp, "12345678", s.StoreID())
	if e != nil {
		t.Fatal(e)
	}
	out, e = w.Retirements()
	if e != nil || len(out) != 1 {
		t.Fatal("outbox lost across restart", out, e)
	}
	response, e := s.RetireRemote("12345678", s.Run(), out[0])
	if e != nil || !response.Ack {
		t.Fatal(e)
	}
	if e = w.CompleteRetirement(response); e != nil {
		t.Fatal(e)
	}
	if e = w.CompleteRetirement(response); e != nil {
		t.Fatal("duplicate ack", e)
	}
	entries, e := w.Entries()
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
	held, e := s.Held("mail", "12345678")
	if e != nil || held != (pool.Amount{}) {
		t.Fatal(held, e)
	}
	if e = s.Audit(); e != nil {
		t.Fatal(e)
	}
	if e = w.Accept(offer); e == nil {
		t.Fatal("retired grant resurrected")
	}
}
