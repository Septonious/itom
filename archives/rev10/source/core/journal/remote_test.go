package journal

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func remoteTest(t *testing.T, s *Store, account, user string, limit pool.Amount) Grant {
	t.Helper()
	enabled(t, s, account, user)
	g, e := s.IssueRemote(Scope{Account: account, User: user, Session: wire.ID{7}}, limit)
	if e != nil {
		t.Fatal(e)
	}
	return g
}
func descriptor(t *testing.T, g Grant, ordinal, previousBytes uint64) (wire.UsageReport, []byte) {
	t.Helper()
	body, e := wire.BindPhysical(quotaKey(g), ordinal, wire.ID{byte(ordinal)}, []byte("encrypted-batch"))
	if e != nil {
		t.Fatal(e)
	}
	d := wire.PhysicalDescriptor{Batch: wire.ID{byte(ordinal)}, EncodedBytes: 1000, DecodedBytes: uint64(len(body)), Digest: sha256.Sum256(body), Folder: "itom-u1-up"}
	return wire.UsageReport{Key: quotaKey(g), Total: wire.Quota{Bytes: previousBytes + 1000, Messages: ordinal}, Attempt: &d}, body
}
func applyTest(t *testing.T, s *Store, m wire.Control) wire.CleanupStatus {
	t.Helper()
	out, e := s.ApplyRemote("12345678", s.Run(), m)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func attemptOrdinal(t *testing.T, s *Store, n uint64) Attempt {
	t.Helper()
	rows, e := s.ScanAttempts(0, 256)
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range rows {
		if a.Ordinal == n {
			return a
		}
	}
	t.Fatal("missing ordinal", n)
	return Attempt{}
}
func cleanRemote(t *testing.T, s *Store, a Attempt, body []byte) {
	t.Helper()
	loc := Location{UIDValidity: 11, UID: uint32(a.Ordinal)}
	if e := s.Observe(a.Ref, s.Run(), Observation{EncodedBytes: 1000, Account: a.Ref.Grant.Scope.Account, Folder: a.Folder, Location: loc, Decoded: body}); e != nil {
		t.Fatal(e)
	}
	if e := s.Deleted(a.Ref, s.Run(), loc); e != nil {
		t.Fatal(e)
	}
}

func TestUnifiedLocalAndRemoteBudgetsOnTenAccounts(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(10))
	for i := 0; i < 10; i++ {
		account := fmt.Sprintf("a%d", i)
		for _, user := range []string{"12345678", "87654321"} {
			remoteTest(t, s, account, user, pool.Amount{Bytes: 6000, Messages: 6})
			issueTest(t, s, account, user, pool.Amount{Bytes: 4000, Messages: 4})
			if _, e := s.IssueRemote(Scope{Account: account, User: user, Session: wire.ID{8}}, pool.Amount{Bytes: 1, Messages: 1}); e != ErrLimit {
				t.Fatal(e)
			}
			heldTest(t, s, account, user, pool.Amount{Bytes: 10000, Messages: 10})
		}
		heldTest(t, s, account, "", pool.Amount{Bytes: 20000, Messages: 20})
	}
}
func TestRemoteRestartKeepsUnreportedFullExposure(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, p, c)
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	run := s.Run()
	offer, e := s.RemoteOffer(g.Ref)
	if e != nil || offer.Key != quotaKey(g) {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, p, c)
	if e = s.CloseGrant(g.Ref); e != ErrScope {
		t.Fatal("local close released remote grant", e)
	}
	if _, e = s.RemoteOffer(g.Ref); e != ErrGeneration {
		t.Fatal(e)
	}
	if _, e = s.ApplyRemote("12345678", run, wire.StorageRelease{Key: quotaKey(g)}); e != ErrGeneration {
		t.Fatal(e)
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
	// The other user's share remains available. New inventory doesn't erase debt.
	remoteTest(t, s, "a0", "87654321", pool.Amount{Bytes: 10000, Messages: 10})
	enabled(t, s, "a0", "12345678")
	if _, e = s.IssueRemote(g.Ref.Scope, pool.Amount{Bytes: 1, Messages: 1}); e != ErrLimit {
		t.Fatal(e)
	}
	// Authenticated final report under the new server Run releases only unused.
	applyTest(t, s, wire.StorageRelease{Key: quotaKey(g), Final: wire.Quota{Bytes: 2000, Messages: 2}})
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 2000, Messages: 2})
}
func TestRemoteReleaseBeforeUsageAndDescriptors(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	final := wire.StorageRelease{Key: quotaKey(g), Final: wire.Quota{Bytes: 2000, Messages: 2}}
	applyTest(t, s, final)
	applyTest(t, s, final)
	applyTest(t, s, wire.UsageReport{Key: quotaKey(g), Total: wire.Quota{Bytes: 1000, Messages: 1}})
	r1, b1 := descriptor(t, g, 1, 0)
	r2, b2 := descriptor(t, g, 2, 1000)
	if _, e := s.ApplyRemote("12345678", s.Run(), r2); e != ErrOrder {
		t.Fatal("gap accepted", e)
	}
	applyTest(t, s, r1)
	applyTest(t, s, r2)
	cleanRemote(t, s, attemptOrdinal(t, s, 2), b2)
	status := applyTest(t, s, r2)
	if status.Registered.Messages != 2 || status.Released.Messages != 1 || !status.Closed {
		t.Fatal(status)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
	cleanRemote(t, s, attemptOrdinal(t, s, 1), b1)
	if status, e := s.ApplyRemote("12345678", s.Run(), r1); e != nil || status.Released != status.Used {
		t.Fatal(e)
	}
	if status, e := s.ApplyRemote("12345678", s.Run(), final); e != nil || !status.Closed {
		t.Fatal(e)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
}
func TestRemoteOpenGrantCleanupDoesNotRenewSpend(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, p, c)
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 2000, Messages: 2})
	r1, b1 := descriptor(t, g, 1, 0)
	applyTest(t, s, r1)
	a := attemptOrdinal(t, s, 1)
	cleanRemote(t, s, a, b1)
	if e := s.Deleted(a.Ref, s.Run(), Location{UIDValidity: 11, UID: 1}); e != ErrMissing {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, p, c)
	status := applyTest(t, s, r1)
	if status.Registered.Messages != 1 || status.Released.Messages != 1 {
		t.Fatal(status)
	}
	rows, e := s.ScanAttempts(0, 256)
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	r2, b2 := descriptor(t, g, 2, 1000)
	applyTest(t, s, r2)
	cleanRemote(t, s, attemptOrdinal(t, s, 2), b2)
	heldTest(t, s, "a0", "12345678", pool.Amount{})
	r3, _ := descriptor(t, g, 3, 2000)
	if _, e = s.ApplyRemote("12345678", s.Run(), r3); e != ErrOrder {
		t.Fatal("cleaned quota renewed grant", e)
	}
	applyTest(t, s, wire.StorageRelease{Key: quotaKey(g), Final: wire.Quota{Bytes: 2000, Messages: 2}})
	if _, e = s.RetireRemote("12345678", s.Run(), wire.Retirement{Key: quotaKey(g), Final: wire.Quota{Bytes: 2000, Messages: 2}}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetGrant(g.Ref); e != ErrMissing {
		t.Fatal(e)
	}
}
func TestRemoteCounterAndAuthorityValidation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	applyTest(t, s, wire.UsageReport{Key: quotaKey(g), Total: wire.Quota{Bytes: 2000, Messages: 2}})
	for _, n := range []wire.Quota{{Bytes: 3000, Messages: 1}, {Bytes: 1000, Messages: 3}, {Bytes: 3000, Messages: 2}, {Bytes: 2000, Messages: 3}, {Bytes: 10001, Messages: 10}} {
		if _, e := s.ApplyRemote("12345678", s.Run(), wire.UsageReport{Key: quotaKey(g), Total: n}); e != ErrOrder {
			t.Fatal(n, e)
		}
	}
	for _, mutate := range []func(*wire.QuotaKey){func(k *wire.QuotaKey) { k.Store[0] ^= 1 }, func(k *wire.QuotaKey) { k.Epoch++ }, func(k *wire.QuotaKey) { k.Session[0] ^= 1 }, func(k *wire.QuotaKey) { k.Account = "a1" }, func(k *wire.QuotaKey) { k.User = "87654321" }} {
		k := quotaKey(g)
		mutate(&k)
		if _, e := s.ApplyRemote("12345678", s.Run(), wire.StorageRelease{Key: k}); e != ErrScope {
			t.Fatal(k, e)
		}
	}
	if _, e := s.ApplyRemote("87654321", s.Run(), wire.StorageRelease{Key: quotaKey(g)}); e != ErrScope {
		t.Fatal(e)
	}
	if _, e := s.ApplyRemote("12345678", s.Run(), cleanupStatus(g)); e != ErrScope {
		t.Fatal("peer cleanup accepted", e)
	}
	if _, e := s.ApplyRemote("12345678", s.Run(), wire.StorageRelease{Key: quotaKey(g), Final: wire.Quota{Bytes: 1000, Messages: 1}}); e != ErrOrder {
		t.Fatal(e)
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
}
func TestRemotePhysicalBindingPreventsDoubleCleanup(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	r1, b1 := descriptor(t, g, 1, 0)
	r2, _ := descriptor(t, g, 2, 1000)
	// A peer copies both the digest and metadata of attempt 1 into ordinal 2.
	*r2.Attempt = *r1.Attempt
	applyTest(t, s, r1)
	applyTest(t, s, r2)
	a2 := attemptOrdinal(t, s, 2)
	e := s.Observe(a2.Ref, s.Run(), Observation{EncodedBytes: 1000, Account: "a0", Folder: a2.Folder, Location: Location{UIDValidity: 11, UID: 1}, Decoded: b1})
	if e != ErrScope {
		t.Fatal("one physical message proved two ordinals", e)
	}
	cleanRemote(t, s, attemptOrdinal(t, s, 1), b1)
	if e = s.Deleted(a2.Ref, s.Run(), Location{UIDValidity: 11, UID: 1}); e != ErrScope {
		t.Fatal(e)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 9000, Messages: 9})
}
func TestRemoteLocalWriterAPIsCannotReleaseOrAuthorize(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	r, _ := descriptor(t, g, 1, 0)
	applyTest(t, s, r)
	a := attemptOrdinal(t, s, 1)
	if _, e := s.Prepare(g.Ref, "itom-u1-up", wire.ID{2}, 100, []byte("x")); e != ErrScope {
		t.Fatal(e)
	}
	if _, e := s.Start(a.Ref); e != ErrScope {
		t.Fatal(e)
	}
	if e := s.CancelPrepared(a.Ref); e != ErrScope {
		t.Fatal(e)
	}
	if e := s.Complete(a.Ref, RejectedBeforeCommit); e != ErrScope {
		t.Fatal(e)
	}
	if e := s.CloseGrant(g.Ref); e != ErrScope {
		t.Fatal(e)
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
}
func TestRemoteLostCommitAcknowledgement(t *testing.T) {
	for _, stage := range []string{"issue", "descriptor", "release", "delete"} {
		t.Run(stage, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "state.db")
			c := testConfig(1)
			s := openTest(t, p, c)
			g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
			r, body := descriptor(t, g, 1, 0)
			var a Attempt
			if stage == "delete" {
				applyTest(t, s, r)
				a = attemptOrdinal(t, s, 1)
				if e := s.Observe(a.Ref, s.Run(), Observation{EncodedBytes: 1000, Account: "a0", Folder: a.Folder, Location: Location{UIDValidity: 11, UID: 1}, Decoded: body}); e != nil {
					t.Fatal(e)
				}
			}
			s.afterCommit = func() error { return errors.New("lost fsync acknowledgement") }
			var e error
			switch stage {
			case "issue":
				enabled(t, s, "a0", "87654321")
				out, err := s.IssueRemote(Scope{Account: "a0", User: "87654321", Session: wire.ID{9}}, g.Limit)
				e = err
				if out != (Grant{}) {
					t.Fatal("leaked uncertain grant")
				}
			case "descriptor":
				out, err := s.ApplyRemote("12345678", s.Run(), r)
				e = err
				if out != (wire.CleanupStatus{}) {
					t.Fatal("leaked uncertain ACK")
				}
			case "release":
				_, e = s.ApplyRemote("12345678", s.Run(), wire.StorageRelease{Key: quotaKey(g), Final: r.Total})
			case "delete":
				e = s.Deleted(a.Ref, s.Run(), Location{UIDValidity: 11, UID: 1})
			}
			if e == nil {
				t.Fatal("lost ack passed")
			}
			if _, e = s.RemoteStatus(g.Ref); e != ErrPoisoned {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s = openTest(t, p, c)
			switch stage {
			case "issue":
				heldTest(t, s, "a0", "", pool.Amount{Bytes: 20000, Messages: 20})
			case "descriptor":
				applyTest(t, s, r)
				rows, err := s.ScanAttempts(0, 256)
				if err != nil || len(rows) != 1 {
					t.Fatal(rows, err)
				}
			case "release":
				heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
			case "delete":
				applyTest(t, s, r)
				rows, err := s.ScanAttempts(0, 256)
				if err != nil || len(rows) != 0 {
					t.Fatal(rows, err)
				}
				heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 9000, Messages: 9})
			}
			if e = s.Audit(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRemoteConcurrentMixedReservation(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	enabled(t, s, "a0", "12345678")
	var wg sync.WaitGroup
	results := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			scope := Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}
			var e error
			if i%2 == 0 {
				_, e = s.Issue(scope, pool.Amount{Bytes: 1000, Messages: 1})
			} else {
				_, e = s.IssueRemote(scope, pool.Amount{Bytes: 1000, Messages: 1})
			}
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	n := 0
	for e := range results {
		if e == nil {
			n++
		} else if e != ErrLimit {
			t.Fatal(e)
		}
	}
	if n != 10 {
		t.Fatal(n)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
}
func TestRemoteMetadataLimitRollbackAndUserIsolation(t *testing.T) {
	c := testConfig(1)
	c.MaxAttempts = 2
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), c)
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	r1, _ := descriptor(t, g, 1, 0)
	r2, _ := descriptor(t, g, 2, 1000)
	applyTest(t, s, r1)
	if _, e := s.ApplyRemote("12345678", s.Run(), r2); e != ErrLimit {
		t.Fatal(e)
	}
	status, e := s.RemoteStatus(g.Ref)
	if e != nil || status.Registered.Messages != 1 {
		t.Fatal(status, e)
	}
	g2 := remoteTest(t, s, "a0", "87654321", pool.Amount{Bytes: 10000, Messages: 10})
	r, _ := descriptor(t, g2, 1, 0)
	r.Attempt.Folder = "itom-u2-up"
	if _, e = s.ApplyRemote("87654321", s.Run(), r); e != nil {
		t.Fatal(e)
	}
	if e = s.Audit(); e != nil {
		t.Fatal(e)
	}
}
func TestJournalSchemaOneUpgradePreservesLocalDebt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, p, c)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	a := prepareTest(t, s, g)
	if _, e := s.Start(a.Ref); e != nil {
		t.Fatal(e)
	}
	// Recreate the actual schema-1 record shape, with no new remote fields.
	e := s.db.Update(func(tx *bolt.Tx) error {
		if e := putNumber(tx.Bucket(metaBucket), "schema", 1); e != nil {
			return e
		}
		for _, b := range [][]byte{grantBucket, attemptBucket} {
			var edits = map[string][]byte{}
			if e := tx.Bucket(b).ForEach(func(k, v []byte) error {
				var obj map[string]json.RawMessage
				if e := json.Unmarshal(v, &obj); e != nil {
					return e
				}
				delete(obj, "Remote")
				delete(obj, "Registered")
				delete(obj, "Ordinal")
				raw, e := json.Marshal(obj)
				edits[string(k)] = raw
				return e
			}); e != nil {
				return e
			}
			for k, v := range edits {
				if e := tx.Bucket(b).Put([]byte(k), v); e != nil {
					return e
				}
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, p, c)
	heldTest(t, s, "a0", "12345678", g.Limit)
	got, e := s.GetAttempt(a.Ref)
	if e != nil || got.State != Unknown || got.Ordinal != 0 {
		t.Fatal(got, e)
	}
	if e = s.CloseGrant(g.Ref); e != nil {
		t.Fatal(e)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
}

func TestRemoteCrashChild(t *testing.T) {
	path := os.Getenv("ITOM_REMOTE_CRASH_PATH")
	if path == "" {
		return
	}
	s := openTest(t, path, testConfig(1))
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	if os.Getenv("ITOM_REMOTE_CRASH_STAGE") == "cleaned" {
		r, body := descriptor(t, g, 1, 0)
		applyTest(t, s, r)
		cleanRemote(t, s, attemptOrdinal(t, s, 1), body)
	}
	os.Exit(0) // deliberately no Close, not a filesystem power-cut simulation
}
func TestRemoteProcessDeathPreservesQuotaAndCleanupPrefix(t *testing.T) {
	for _, stage := range []string{"issued", "cleaned"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			cmd := exec.Command(os.Args[0], "-test.run=^TestRemoteCrashChild$")
			cmd.Env = append(os.Environ(), "ITOM_REMOTE_CRASH_PATH="+path, "ITOM_REMOTE_CRASH_STAGE="+stage)
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("child %v: %s", e, out)
			}
			s := openTest(t, path, testConfig(1))
			grants, e := s.ScanGrants(0, 2)
			if e != nil || len(grants) != 1 {
				t.Fatal(grants, e)
			}
			g := grants[0]
			want := g.Limit
			if stage == "cleaned" {
				want = pool.Amount{Bytes: 9000, Messages: 9}
				r, _ := descriptor(t, g, 1, 0)
				status := applyTest(t, s, r)
				if status.Used.Messages != 1 || status.Registered.Messages != 1 || status.Released.Messages != 1 {
					t.Fatal(status)
				}
			}
			rows, e := s.ScanAttempts(0, 2)
			if e != nil || len(rows) != 0 {
				t.Fatal(rows, e)
			}
			heldTest(t, s, "a0", "12345678", want)
		})
	}
}

func TestRemoteObservedSizeAndFailedTransaction(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, p, c)
	g := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	r, body := descriptor(t, g, 1, 0)
	s.beforeCommit = func() error { return errors.New("failed transaction") }
	if status, e := s.ApplyRemote("12345678", s.Run(), r); e == nil || status != (wire.CleanupStatus{}) {
		t.Fatal(status, e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s = openTest(t, p, c)
	status, e := s.RemoteStatus(g.Ref)
	if e != nil || status.Registered.Messages != 0 || status.Used.Messages != 0 {
		t.Fatal(status, e)
	}
	applyTest(t, s, r)
	a := attemptOrdinal(t, s, 1)
	for _, size := range []uint64{0, 999, 1001} {
		e = s.Observe(a.Ref, s.Run(), Observation{Account: "a0", Folder: a.Folder, EncodedBytes: size, Location: Location{UIDValidity: 11, UID: 1}, Decoded: body})
		if e != ErrScope {
			t.Fatal("accepted mismatched stored size", size, e)
		}
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
}

func TestHeldByOriginKeepsUnprovedRemoteDebt(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "origin.db"), testConfig(2))
	issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 2000, Messages: 2})
	remote := remoteTest(t, s, "a0", "12345678", pool.Amount{Bytes: 6000, Messages: 6})
	report, body := descriptor(t, remote, 1, 0)
	applyTest(t, s, report)
	// Authenticated closure releases unused allocation, but no physical refund.
	applyTest(t, s, wire.StorageRelease{Key: quotaKey(remote), Final: report.Total})
	local, inbound, err := s.HeldByOrigin("a0", "12345678")
	if err != nil || local != (pool.Amount{Bytes: 2000, Messages: 2}) || inbound != (pool.Amount{Bytes: 1000, Messages: 1}) {
		t.Fatal(local, inbound, err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 3000, Messages: 3})
	cleanRemote(t, s, attemptOrdinal(t, s, 1), body)
	local, inbound, err = s.HeldByOrigin("a0", "12345678")
	if err != nil || local != (pool.Amount{Bytes: 2000, Messages: 2}) || inbound != (pool.Amount{}) {
		t.Fatal(local, inbound, err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 2000, Messages: 2})
	if _, _, err = s.HeldByOrigin("a0", "unknown"); err != ErrScope {
		t.Fatal("foreign origin scope", err)
	}
}
