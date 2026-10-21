package tunnel

import (
	"bytes"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestField68InvalidDataProofDoesNotCloseHealthyCarrierOrRefund(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, e := ledger(t.TempDir(), "data", a, u, false)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.EnableAfterInventory(a.ID, u.ID, s.Run()); e != nil {
		t.Fatal(e)
	}
	w, e := wallet.Open(t.TempDir()+"/wallet.db", u.ID, s.StoreID())
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	grant, e := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, dataProfile12)
	if e != nil {
		t.Fatal(e)
	}
	offer, e := s.RemoteOffer(grant.Ref)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Accept(offer); e != nil {
		t.Fatal(e)
	}
	report, body, e := w.Spend(offer.Key, u.Up, bytes.Repeat([]byte{0x34}, 2048), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ApplyRemote(u.ID, s.Run(), report); e != nil {
		t.Fatal(e)
	}
	raw, e := mailbox.Encode(body)
	if e != nil {
		t.Fatal(e)
	}
	before, e := s.RemoteStatus(grant.Ref)
	if e != nil {
		t.Fatal(e)
	}
	// Build a real Noise epoch bound to the wallet session, not a synthetic
	// cipher for a different session. This DATA is intentionally *not* ITD3.
	cipher := testField68ServerEpoch(t, w.Session())
	l := &lane{data: s, cipher: cipher, side: true, reports: map[[32]byte]wire.UsageReport{report.Attempt.Digest: report}, pending: map[[32]byte]mailbox.Message{}, remoteGC: map[journal.AttemptRef]remoteCleanup{}}
	msg := mailbox.Message{Folder: u.Up, UID: 1, Validity: 1, MIME: raw}
	// Exercise the real reader's single-decode fast path, with exactly the
	// same invalid physical evidence and journal state as the legacy fallback.
	decoded, decodeErr := mailbox.Decode(raw)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	e = l.remoteDataPrepared(msg, report, decoded, true)
	clear(decoded)
	if e != nil {
		t.Fatalf("one corrupted batch disconnected carrier: %v", e)
	}
	if len(l.badDataProofs) != 1 || len(l.remoteGC) != 0 || len(l.reports) != 1 {
		t.Fatalf("invalid proof handled unsafely: quarantine=%d gc=%d reports=%d", len(l.badDataProofs), len(l.remoteGC), len(l.reports))
	}
	if !l.dataProofQuarantined(report.Attempt.Digest) {
		t.Fatal("bad physical proof immediately retried")
	}
	after, e := s.RemoteStatus(grant.Ref)
	if e != nil || after.Registered != before.Registered || after.Released != before.Released || after.Used != before.Used {
		t.Fatalf("proof failure refunded debt: before=%+v after=%+v e=%v", before, after, e)
	}
	// A corrupt duplicate does not add extra cleanup. TTL re-enables a probe.
	if e = l.remoteData(msg, report); e != nil || len(l.remoteGC) != 0 {
		t.Fatal("corrupt duplicate modified journal", e)
	}
	l.badDataProofs[report.Attempt.Digest] = time.Now().Add(-time.Second)
	if l.dataProofQuarantined(report.Attempt.Digest) {
		t.Fatal("expired quarantine never retries")
	}
	if e = l.remoteData(msg, report); e != nil || len(l.remoteGC) != 0 {
		t.Fatalf("legacy stored-row fallback changed journal after TTL: %v", e)
	}
	for i := 0; i < 200; i++ {
		key := [32]byte{byte(i), byte(i >> 8)}
		l.quarantineDataProof(key)
	}
	if len(l.badDataProofs) > invalidDataProofLimit {
		t.Fatalf("unbounded quarantine: %d", len(l.badDataProofs))
	}
	if len(l.reports) != 0 {
		t.Fatalf("eviction retained stale descriptor cache: %d", len(l.reports))
	}
	if e = s.Audit(); e != nil {
		t.Fatal(e)
	}
}

// Simulate a coarse-resolution Windows clock that returns exactly the same
// timestamp for every quarantine. Eviction must still remove the oldest proof
// AND its descriptor cache; physical journal debt must not be fabricated away.
func TestField68QuarantineEvictsOldestOnIdenticalClockTicks(t *testing.T) {
	l := &lane{reports: make(map[[32]byte]wire.UsageReport)}
	fixed := time.Unix(12345, 0)
	oldest := [32]byte{0xFF, 0xFF, 0xFF}
	l.reports[oldest] = wire.UsageReport{}
	l.quarantineDataProofAt(oldest, fixed)
	previous := l.badDataProofs[oldest]
	for i := 0; i < invalidDataProofLimit+50; i++ {
		key := [32]byte{byte(i), byte(i >> 8)}
		l.quarantineDataProofAt(key, fixed)
		if !l.badDataProofs[key].After(previous) {
			t.Fatalf("timestamp order did not advance at %d", i)
		}
		previous = l.badDataProofs[key]
		if len(l.badDataProofs) > invalidDataProofLimit {
			t.Fatalf("quarantine cache exceeded limit: %d", len(l.badDataProofs))
		}
	}
	if _, exists := l.badDataProofs[oldest]; exists {
		t.Fatal("oldest bad proof remained in bounded quarantine")
	}
	if _, exists := l.reports[oldest]; exists {
		t.Fatal("evicting old proof retained its non-durable descriptor cache")
	}
}

func testField68ServerEpoch(t *testing.T, sid wire.ID) *secure.Epoch {
	t.Helper()
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: sid, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, e := secure.NewHandshake(binding, make([]byte, 32), true)
	if e != nil {
		t.Fatal(e)
	}
	two, e := secure.NewHandshake(binding, make([]byte, 32), false)
	if e != nil {
		t.Fatal(e)
	}
	msg, e := one.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = two.Read(msg); e != nil {
		t.Fatal(e)
	}
	msg, e = two.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = one.Read(msg); e != nil {
		t.Fatal(e)
	}
	server, e := two.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	return server
}
