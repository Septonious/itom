package journal

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/pool"
	"itom.local/core/wire"
)

func testConfig(accounts int) Config {
	c := Config{MaxGrants: 64, MaxAttempts: 128}
	for i := 0; i < accounts; i++ {
		c.Accounts = append(c.Accounts, Account{ID: fmt.Sprintf("a%d", i), Provider: "test-imap",
			Limit: pool.Amount{Bytes: 20000, Messages: 20}, Users: []User{
				{ID: "12345678", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"itom-u1-up", "itom-u1-down"}},
				{ID: "87654321", Limit: pool.Amount{Bytes: 10000, Messages: 10}, Folders: []string{"itom-u2-up", "itom-u2-down"}},
			}})
	}
	return c
}
func openTest(t *testing.T, path string, c Config) *Store {
	t.Helper()
	s, err := Open(path, c, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func enabled(t *testing.T, s *Store, account, user string) {
	t.Helper()
	if err := s.EnableAfterInventory(account, user, s.Run()); err != nil {
		t.Fatal(err)
	}
}
func issueTest(t *testing.T, s *Store, account, user string, limit pool.Amount) Grant {
	t.Helper()
	enabled(t, s, account, user)
	g, err := s.Issue(Scope{Account: account, User: user, Session: wire.ID{1}}, limit)
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func prepareTest(t *testing.T, s *Store, g Grant) Attempt {
	t.Helper()
	var batch wire.ID
	if _, err := rand.Read(batch[:]); err != nil {
		t.Fatal(err)
	}
	a, err := s.Prepare(g.Ref, "itom-u1-up", batch, 1000, []byte("canonical-envelope"))
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func heldTest(t *testing.T, s *Store, account, user string, want pool.Amount) {
	t.Helper()
	got, err := s.Held(account, user)
	if err != nil || got != want {
		t.Fatal(got, want, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
func observation() Observation {
	return Observation{Account: "a0", Folder: "itom-u1-up", Location: Location{UIDValidity: 10, UID: 3}, Decoded: []byte("canonical-envelope")}
}

func TestCompleteInventoryAbsenceReleasesOnlyLocalPhysicalAttempt(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if _, err := s.Start(a.Ref); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(a.Ref, Uncertain); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
	if err := s.AbsentFromCompleteInventory(a.Ref, s.Run(), "itom-u1-down"); !errors.Is(err, ErrScope) {
		t.Fatalf("wrong folder released physical debt: %v", err)
	}
	if err := s.AbsentFromCompleteInventory(a.Ref, s.Run()+1, "itom-u1-up"); !errors.Is(err, ErrGeneration) {
		t.Fatalf("stale run released physical debt: %v", err)
	}
	if err := s.AbsentFromCompleteInventory(a.Ref, s.Run(), "itom-u1-up"); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
}

func TestDurableGrantRestartAndPerOwnerAdmission(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, path, c)
	if _, err := s.Issue(Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 1000, Messages: 1}); !errors.Is(err, ErrRecovery) {
		t.Fatal(err)
	}
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
	oldRun := s.Run()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path, c)
	if s.Run() <= oldRun {
		t.Fatal("run reused")
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
	if err := s.EnableAfterInventory("a0", "12345678", oldRun); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	// One unresolved user's full allocation cannot consume the other's share.
	other := issueTest(t, s, "a0", "87654321", pool.Amount{Bytes: 10000, Messages: 10})
	heldTest(t, s, "a0", "", pool.Amount{Bytes: 20000, Messages: 20})
	enabled(t, s, "a0", "12345678")
	if _, err := s.Prepare(g.Ref, "itom-u1-up", wire.ID{2}, 100, []byte("x")); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	if other.Ref.ID <= g.Ref.ID {
		t.Fatal("grant ID reused")
	}
}
func TestTenAccountsSharedByBothUsers(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(10))
	for i := 0; i < 10; i++ {
		account := fmt.Sprintf("a%d", i)
		for _, user := range []string{"12345678", "87654321"} {
			issueTest(t, s, account, user, pool.Amount{Bytes: 10000, Messages: 10})
		}
		heldTest(t, s, account, "", pool.Amount{Bytes: 20000, Messages: 20})
	}
}
func TestIntentStartOnceAndReleaseOnlyAfterConcreteCleanup(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if _, err := s.Start(a.Ref); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(a.Ref); !errors.Is(err, ErrState) {
		t.Fatal("repeated authorization", err)
	}
	if err := s.Complete(a.Ref, Uncertain); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
	if err := s.Complete(a.Ref, RejectedBeforeCommit); !errors.Is(err, ErrState) {
		t.Fatal("unknown erased", err)
	}
	if err := s.Deleted(a.Ref, s.Run(), observation().Location); !errors.Is(err, ErrScope) {
		t.Fatal("unproved cleanup", err)
	}
	if err := s.Observe(a.Ref, s.Run(), observation()); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted(a.Ref, s.Run(), observation().Location); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
	if err := s.Deleted(a.Ref, s.Run(), observation().Location); !errors.Is(err, ErrMissing) {
		t.Fatal("cleanup replay", err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
}
func TestScopeDigestAndUIDValidityRejectForeignCleanup(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if _, err := s.Start(a.Ref); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Observation){
		func(o *Observation) { o.Account = "other" }, func(o *Observation) { o.Folder = "INBOX" },
		func(o *Observation) { o.Folder = "itom-u2-up" }, func(o *Observation) { o.Decoded = []byte("different-envelope") },
		func(o *Observation) { o.Location.UIDValidity = 0 },
	} {
		o := observation()
		change(&o)
		if err := s.Observe(a.Ref, s.Run(), o); !errors.Is(err, ErrScope) {
			t.Fatal(err)
		}
	}
	if err := s.Observe(a.Ref, s.Run(), observation()); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted(a.Ref, s.Run(), Location{UIDValidity: 11, UID: 3}); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	forged := a.Ref
	forged.Grant.Scope.User = "87654321"
	if _, err := s.GetAttempt(forged); !errors.Is(err, ErrScope) {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
}
func TestPreparedCancelAndDefinitiveNoCommit(t *testing.T) {
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), testConfig(1))
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if err := s.CancelPrepared(a.Ref); err != nil {
		t.Fatal(err)
	}
	b := prepareTest(t, s, g)
	if b.Ref.ID <= a.Ref.ID {
		t.Fatal("attempt reused")
	}
	if _, err := s.Start(b.Ref); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelPrepared(b.Ref); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if err := s.Complete(b.Ref, RejectedBeforeCommit); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
}
func TestUnknownRestartRetainsDebtAndRejectsOldWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, path, c)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if _, err := s.Start(a.Ref); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path, c)
	if err := s.Complete(a.Ref, RejectedBeforeCommit); !errors.Is(err, ErrGeneration) {
		t.Fatal(err)
	}
	if err := s.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1000, Messages: 1})
	if err := s.Observe(a.Ref, s.Run(), observation()); err != nil {
		t.Fatal(err)
	}
	if err := s.Deleted(a.Ref, s.Run(), observation().Location); err != nil {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{})
}
func TestCommitFailurePoisonsAndLostAcknowledgementRetainsIntent(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			c := testConfig(1)
			s := openTest(t, path, c)
			g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
			fault := func() error { return errors.New("injected disk/completion fault") }
			if after {
				s.afterCommit = fault
			} else {
				s.beforeCommit = fault
			}
			a, err := s.Prepare(g.Ref, "itom-u1-up", wire.ID{2}, 1000, []byte("canonical-envelope"))
			if err == nil || a.Ref.ID != 0 {
				t.Fatal("leaked unacknowledged intent", a, err)
			}
			if _, err = s.Issue(g.Ref.Scope, pool.Amount{Bytes: 1, Messages: 1}); !errors.Is(err, ErrPoisoned) {
				t.Fatal(err)
			}
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s = openTest(t, path, c)
			entries, err := s.ScanAttempts(0, 256)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if after {
				want = 1
			}
			if len(entries) != want {
				t.Fatal(entries)
			}
			recovered, err := s.GetGrant(g.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Used.Messages != uint64(want) {
				t.Fatal("non-atomic consumption", recovered)
			}
			heldTest(t, s, "a0", "12345678", g.Limit)
		})
	}
}
func TestLostStartAcknowledgementNeverReauthorizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, path, c)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	s.afterCommit = func() error { return errors.New("lost START completion") }
	permit, err := s.Start(a.Ref)
	if err == nil || permit.Ref.ID != 0 {
		t.Fatal("permission leaked")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTest(t, path, c)
	enabled(t, s, "a0", "12345678")
	if _, err = s.Start(a.Ref); !errors.Is(err, ErrGeneration) {
		t.Fatal("old intent reauthorized", err)
	}
	if err = s.CancelPrepared(a.Ref); !errors.Is(err, ErrState) {
		t.Fatal("uncertainty cleared", err)
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
}
func TestExclusiveLockAndChangedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, path, c)
	other, err := Open(path, c, 25*time.Millisecond)
	if err == nil {
		_ = other.Close()
		t.Fatal("second owner")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	changed := testConfig(1)
	changed.Accounts[0].Users[0].Limit.Bytes--
	other, err = Open(path, changed, 25*time.Millisecond)
	if !errors.Is(err, ErrConfig) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal(err)
	}
	s = openTest(t, path, c)
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
func TestBoundedAdmissionAndCursorCopies(t *testing.T) {
	c := testConfig(1)
	c.MaxGrants = 2
	c.MaxAttempts = 4
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), c)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	if _, err := s.Issue(g.Ref.Scope, pool.Amount{Bytes: 1, Messages: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	a := prepareTest(t, s, g)
	b := prepareTest(t, s, g)
	if _, err := s.Prepare(g.Ref, "itom-u1-up", wire.ID{4}, 1000, []byte("x")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	page, err := s.ScanAttempts(0, 1)
	if err != nil || len(page) != 1 || page[0].Ref != a.Ref {
		t.Fatal(page, err)
	}
	page[0].Folder = "changed"
	page, err = s.ScanAttempts(a.Ref.ID, 1)
	if err != nil || len(page) != 1 || page[0].Ref != b.Ref {
		t.Fatal(page, err)
	}
	original, err := s.GetAttempt(a.Ref)
	if err != nil || original.Folder != "itom-u1-up" {
		t.Fatal(original, err)
	}
	if _, err = s.ScanAttempts(0, 257); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	heldTest(t, s, "a0", "12345678", g.Limit)
}
func TestConcurrentReservationsCannotOversubscribe(t *testing.T) {
	c := testConfig(1)
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), c)
	enabled(t, s, "a0", "12345678")
	var wg sync.WaitGroup
	results := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Issue(Scope{Account: "a0", User: "12345678", Session: wire.ID{1}}, pool.Amount{Bytes: 1000, Messages: 1})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	}
	if success != 10 {
		t.Fatal(success)
	}
	heldTest(t, s, "a0", "12345678", pool.Amount{Bytes: 10000, Messages: 10})
}
func TestMetadataContainsNoPayloadAndRejectsCorruptBalance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	c := testConfig(1)
	s := openTest(t, path, c)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	payload := bytes.Repeat([]byte("PRIVATE_PAYLOAD_"), 32)
	if _, err := s.Prepare(g.Ref, "itom-u1-up", wire.ID{3}, 1000, payload); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, payload) {
		t.Fatal("payload persisted")
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(func(tx *bolt.Tx) error { return setBalance(tx, ownerKey("a0", "12345678"), pool.Amount{}) })
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	bad, err := Open(path, c, time.Second)
	if bad != nil {
		_ = bad.Close()
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}

// Helper subprocess exits without Store.Close to exercise committed metadata
// surviving process death. It does NOT emulate physical power loss or bad fsync.
func TestJournalCrashChild(t *testing.T) {
	path := os.Getenv("ITOM_JOURNAL_CRASH_PATH")
	if path == "" {
		return
	}
	s := openTest(t, path, testConfig(1))
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	a := prepareTest(t, s, g)
	if os.Getenv("ITOM_JOURNAL_CRASH_STAGE") == "started" {
		if _, err := s.Start(a.Ref); err != nil {
			t.Fatal(err)
		}
	}
	os.Exit(0)
}
func TestProcessDeathPreservesPreparedAndStarted(t *testing.T) {
	for _, stage := range []string{"prepared", "started"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			cmd := exec.Command(os.Args[0], "-test.run=^TestJournalCrashChild$")
			cmd.Env = append(os.Environ(), "ITOM_JOURNAL_CRASH_PATH="+path, "ITOM_JOURNAL_CRASH_STAGE="+stage)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child: %v: %s", err, out)
			}
			s := openTest(t, path, testConfig(1))
			entries, err := s.ScanAttempts(0, 2)
			if err != nil || len(entries) != 1 {
				t.Fatal(entries, err)
			}
			a := entries[0]
			if a.Run >= s.Run() {
				t.Fatal("generation not advanced")
			}
			if stage == "started" && a.State != Unknown {
				t.Fatal("started attempt not recovered as unknown")
			}
			if stage == "prepared" {
				if err = s.CancelPrepared(a.Ref); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = s.CancelPrepared(a.Ref); !errors.Is(err, ErrState) {
					t.Fatal(err)
				}
			}
			if err = s.CloseGrant(a.Ref.Grant); err != nil {
				t.Fatal(err)
			}
			want := pool.Amount{}
			if stage == "started" {
				want = pool.Amount{Bytes: 1000, Messages: 1}
			}
			heldTest(t, s, "a0", "12345678", want)
		})
	}
}

func TestMetadataSharesProtectOtherUser(t *testing.T) {
	c := testConfig(1)
	c.MaxGrants = 4
	c.MaxAttempts = 4
	s := openTest(t, filepath.Join(t.TempDir(), "state.db"), c)
	g := issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 5000, Messages: 5})
	issueTest(t, s, "a0", "12345678", pool.Amount{Bytes: 1, Messages: 1})
	if _, err := s.Issue(g.Ref.Scope, pool.Amount{Bytes: 1, Messages: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal("metadata share bypass", err)
	}
	prepareTest(t, s, g)
	prepareTest(t, s, g)
	if _, err := s.Prepare(g.Ref, "itom-u1-up", wire.ID{7}, 1000, []byte("x")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	other := issueTest(t, s, "a0", "87654321", pool.Amount{Bytes: 5000, Messages: 5})
	if _, err := s.Prepare(other.Ref, "itom-u2-up", wire.ID{8}, 1000, []byte("x")); err != nil {
		t.Fatal("other user starved", err)
	}
	if err := s.Audit(); err != nil {
		t.Fatal(err)
	}
}
