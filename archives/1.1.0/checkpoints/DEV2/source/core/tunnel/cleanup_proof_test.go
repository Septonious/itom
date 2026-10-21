package tunnel

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

type batchProofMail struct {
	*memoryMail
	calls, singles int
	maxIDs         int
	err            error
}

func (m *batchProofMail) Present(ctx context.Context, folder string, validity uint32, ids []uint32) (map[uint32]bool, error) {
	m.calls++
	if len(ids) > m.maxIDs {
		m.maxIDs = len(ids)
	}
	if m.err != nil {
		return nil, m.err
	}
	out := map[uint32]bool{}
	for _, uid := range ids {
		absent, err := m.memoryMail.Absent(ctx, folder, validity, uid)
		if err != nil {
			return nil, err
		}
		if !absent {
			out[uid] = true
		}
	}
	return out, nil
}
func (m *batchProofMail) Absent(ctx context.Context, folder string, validity, uid uint32) (bool, error) {
	m.singles++
	return m.memoryMail.Absent(ctx, folder, validity, uid)
}

func TestBoundedCleanupBatchesMissingLocatorsWithoutUnprovedRefund(t *testing.T) {
	for _, fault := range []error{nil, mailbox.ErrChanged, errors.New("provider unavailable")} {
		t.Run(func() string {
			if fault == nil {
				return "complete"
			}
			return fault.Error()
		}(), func(t *testing.T) {
			ctx := context.Background()
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			store, err := ledger(t.TempDir(), "control", a, u, true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
				t.Fatal(err)
			}
			m := &batchProofMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}, err: fault}
			m.Ensure(ctx, u.Down)
			g, err := store.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{1}}, pool.Amount{Bytes: 4096, Messages: 2})
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 2; i++ {
				body := []byte{byte(i)}
				raw, _ := mailbox.Encode(body)
				attempt, err := store.Prepare(g.Ref, u.Down, wire.ID{byte(i)}, uint64(len(raw)), body)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.Start(attempt.Ref); err != nil {
					t.Fatal(err)
				}
				if err = store.Complete(attempt.Ref, journal.Uncertain); err != nil {
					t.Fatal(err)
				}
				if err = store.Observe(attempt.Ref, store.Run(), journal.Observation{Account: a.ID, Folder: u.Down, EncodedBytes: uint64(len(raw)), Location: journal.Location{UIDValidity: 1, UID: uint32(i)}, Decoded: body}); err != nil {
					t.Fatal(err)
				}
				m.Append(ctx, u.Down, raw)
			}
			delete(m.rows[u.Down], 2)
			// Partial page has neither pinned UID; only one is physically absent.
			rows := []mailbox.Message{{Folder: u.Down, Validity: 1, UID: 3, MIME: []byte("unrelated")}}
			gone, err := reclaimBounded(ctx, m, store, rows, func(journal.Attempt) bool { return true }, 32)
			if fault == nil {
				if err != nil || len(gone) != 1 || gone[0].Location.UID != 2 {
					t.Fatal("invalid absence refund", gone, err)
				}
			} else if len(gone) != 0 {
				t.Fatal("fault refunded unproved debt", gone)
			}
			if m.calls != 1 {
				t.Fatal("missing locators did not share one proof", m.calls)
			}
			if fault == nil && m.singles != 0 {
				t.Fatal("serial absence probes remain", m.singles)
			}
			if err = store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type countedCleanupScanMail struct {
	*memoryMail
	pages int
}

func (m *countedCleanupScanMail) Page(ctx context.Context, f string, cur mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	m.pages++
	return m.memoryMail.Page(ctx, f, cur)
}
func TestLocalCleanupSkipsPeerOnlyFolderAndFindsStaleAttempt(t *testing.T) {
	ctx := context.Background()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	store, err := ledger(t.TempDir(), "control", a, u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := &countedCleanupScanMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
	m.Ensure(ctx, u.Down)
	raw, _ := mailbox.Encode(make([]byte, 32<<10))
	for i := 0; i < 600; i++ {
		m.Append(ctx, u.Down, raw)
	}
	binding := secure.Binding{Server: wire.ID{1}, User: u.ID, Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
	two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
	msg, _ := one.Write(nil)
	if err = two.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, _ = two.Write(nil)
	if err = one.Read(msg); err != nil {
		t.Fatal(err)
	}
	cipher, err := one.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: ctx, account: a, user: u, control: store, mail: m, sendFolder: u.Down, cipher: cipher}
	for i := 0; i < 40; i++ {
		r := l.cleanup(cleanupLocalControl, nil, nil, nil, true, store)
		if r.err != nil {
			t.Fatal("peer MIME exhausted local cleanup cache", r.err)
		}
		if len(l.outCache) != 0 {
			t.Fatal("unowned MIME retained", len(l.outCache))
		}
	}
	if l.outCursor.After != 0 || m.pages != 0 {
		t.Fatal("peer-only folder unnecessarily fetched", l.outCursor, m.pages)
	}
	if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
		t.Fatal(err)
	}
	body := []byte("stale-local-attempt")
	encoded, _ := mailbox.Encode(body)
	g, err := store.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{9}}, pool.Amount{Bytes: uint64(len(encoded)), Messages: 1})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := store.Prepare(g.Ref, u.Down, wire.ID{5}, uint64(len(encoded)), body)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Start(attempt.Ref); err != nil {
		t.Fatal(err)
	}
	if err = store.Complete(attempt.Ref, journal.Uncertain); err != nil {
		t.Fatal(err)
	}
	if err = store.CloseGrant(g.Ref); err != nil {
		t.Fatal(err)
	}
	if err = m.Append(ctx, u.Down, encoded); err != nil {
		t.Fatal(err)
	}
	cleaned := 0
	for range 40 {
		result := l.cleanup(cleanupLocalControl, nil, nil, nil, true, store)
		if result.err != nil {
			t.Fatal(result.err)
		}
		cleaned += len(result.local)
	}
	if cleaned != 1 || len(m.rows[u.Down]) != 600 || m.pages == 0 {
		t.Fatal("stale attempt lost or peer removed", cleaned, len(m.rows[u.Down]), m.pages)
	}
	held, err := store.Held(a.ID, u.ID)
	if err != nil || held != (pool.Amount{}) {
		t.Fatal("stale debt leaked", held, err)
	}
	if err = store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestPresenceCleanupBoundsUIDsRatherThanBatchedFrameCounters(t *testing.T) {
	for _, profile := range []struct {
		name                       string
		messages, records, checked int
	}{
		{"batched", 128, 16, 2048}, {"single", 129, 1, 128},
	} {
		t.Run(profile.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := &batchProofMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
			l := &lane{ctx: ctx, mail: m, recvFolder: "itom-u-up", known: map[uint64]mailbox.Message{}, gcDone: make(chan cleanupResult, 1)}
			for uid := 1; uid <= profile.messages; uid++ {
				for record := 0; record < profile.records; record++ {
					counter := uint64((uid-1)*profile.records + record + 1)
					l.known[counter] = mailbox.Message{Validity: 1, UID: uint32(uid)}
				}
			}
			if !l.startCleanup(cleanupPresence) {
				t.Fatal("presence proof did not start")
			}
			l.workers.Wait()
			result := <-l.gcDone
			if result.err != nil || len(result.checked) != profile.checked || m.maxIDs != 128 {
				t.Fatal("physical batch bound or frame coverage wrong", len(result.checked), m.maxIDs, result.err)
			}
			if err := l.finishCleanup(result); err != nil {
				t.Fatal(err)
			}
			if len(l.known) != profile.messages*profile.records-profile.checked {
				t.Fatal("proven absence did not prune exactly checked counters", len(l.known))
			}
		})
	}
}

// The cached path must still physically delete matched UIDs before refunding;
// a failed provider delete keeps all held debt, even though ACK made it eligible.
type cachedCleanupMail struct {
	*memoryMail
	pageCalls int
	deleteErr error
}

func (m *cachedCleanupMail) Page(ctx context.Context, f string, c mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	m.pageCalls++
	return nil, c, errors.New("unexpected folder scan")
}
func (m *cachedCleanupMail) Delete(ctx context.Context, row mailbox.Message) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	return m.memoryMail.Delete(ctx, row)
}
func TestCachedCleanupDrainsBoundedBatchOnlyAfterPhysicalProof(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			ctx := context.Background()
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			store, err := ledger(t.TempDir(), "control", a, u, true)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
				t.Fatal(err)
			}
			g, err := store.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{1}}, pool.Amount{Bytes: 32768, Messages: 40})
			if err != nil {
				t.Fatal(err)
			}
			m := &cachedCleanupMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}
			m.Ensure(ctx, u.Down)
			if fail {
				m.deleteErr = errors.New("provider delete failure")
			}
			binding := secure.Binding{Server: wire.ID{1}, User: u.ID, Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
			one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
			two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
			msg, _ := one.Write(nil)
			if err = two.Read(msg); err != nil {
				t.Fatal(err)
			}
			msg, _ = two.Write(nil)
			if err = one.Read(msg); err != nil {
				t.Fatal(err)
			}
			cipher, err := one.Epoch()
			if err != nil {
				t.Fatal(err)
			}
			l := &lane{ctx: ctx, account: a, user: u, control: store, mail: m, sendFolder: u.Down, cipher: cipher}
			eligible := map[journal.AttemptRef]bool{}
			for i := 1; i <= 40; i++ {
				body := []byte{byte(i)}
				raw, _ := mailbox.Encode(body)
				attempt, err := store.Prepare(g.Ref, u.Down, wire.ID{byte(i)}, uint64(len(raw)), body)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.Start(attempt.Ref); err != nil {
					t.Fatal(err)
				}
				if err = store.Complete(attempt.Ref, journal.Uncertain); err != nil {
					t.Fatal(err)
				}
				m.Append(ctx, u.Down, raw)
				l.outCache = append(l.outCache, mailbox.Message{Folder: u.Down, Validity: 1, UID: uint32(i), MIME: raw})
				eligible[attempt.Ref] = true
			}
			before, _ := store.Held(a.ID, u.ID)
			result := l.cleanup(cleanupLocalControl, eligible, nil, nil, true, store)
			if m.pageCalls != 0 {
				t.Fatal("cached batch performed a folder scan", m.pageCalls)
			}
			after, _ := store.Held(a.ID, u.ID)
			if fail {
				if result.err == nil || len(result.local) != 0 || before != after {
					t.Fatal("failed delete refunded debt", result.err, len(result.local), before, after)
				}
			} else {
				if result.err != nil || len(result.local) != 32 || len(l.outCache) != 8 || after.Messages != before.Messages-32 {
					t.Fatal("wrong bounded physical refund", result.err, len(result.local), len(l.outCache), before, after)
				}
			}
			if err = store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type targetedCleanupMail struct {
	*cachedCleanupMail
	hint  mailbox.Message
	calls int
}

func (m *targetedCleanupMail) FetchCleanupCandidates(ctx context.Context, folder string, wanted map[[32]byte]bool) ([]mailbox.Message, error) {
	m.calls++
	return []mailbox.Message{m.hint}, nil
}

func TestTargetedCleanupRequiresFetchedBodyAndPhysicalDeletion(t *testing.T) {
	for _, mode := range []string{"proved", "forged-body", "foreign-folder", "delete-error", "cached-plus-fetched", "cached-plus-delete-error", "cached-pair-plus-fetched", "cached-pair-delete-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			s, err := ledger(t.TempDir(), "data", a, u, false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			cipher := cleanupFixtureEpoch(t)
			g, err := s.Issue(journal.Scope{Account: a.ID, User: u.ID, Session: cipher.Context().Session}, pool.Amount{Bytes: 1024, Messages: 3})
			if err != nil {
				t.Fatal(err)
			}
			body := []byte{1, 2, 3}
			raw, _ := mailbox.Encode(body)
			attempt, err := s.Prepare(g.Ref, u.Down, wire.ID{1}, uint64(len(raw)), body)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Start(attempt.Ref); err != nil {
				t.Fatal(err)
			}
			if err = s.Complete(attempt.Ref, journal.Accepted); err != nil {
				t.Fatal(err)
			}
			m := &targetedCleanupMail{cachedCleanupMail: &cachedCleanupMail{memoryMail: &memoryMail{rows: map[string]map[uint32][]byte{}, next: map[string]uint32{}}}}
			m.Ensure(ctx, u.Down)
			m.Append(ctx, u.Down, raw)
			m.hint = mailbox.Message{Folder: u.Down, Validity: 1, UID: 1, MIME: raw}
			switch mode {
			case "forged-body":
				m.hint.MIME, _ = mailbox.Encode([]byte{99})
			case "foreign-folder":
				m.hint.Folder = u.Up
			case "delete-error":
				m.deleteErr = errors.New("provider delete failed")
			}
			l := &lane{ctx: ctx, account: a, user: u, data: s, mail: m, sendFolder: u.Down, cipher: cipher}
			eligible := map[journal.AttemptRef]bool{attempt.Ref: true}
			if mode == "cached-plus-fetched" || mode == "cached-plus-delete-error" || mode == "cached-pair-plus-fetched" || mode == "cached-pair-delete-error" {
				l.outCache = []mailbox.Message{m.hint}
				body2 := []byte{4, 5, 6}
				raw2, _ := mailbox.Encode(body2)
				a2, err := s.Prepare(g.Ref, u.Down, wire.ID{2}, uint64(len(raw2)), body2)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.Start(a2.Ref); err != nil {
					t.Fatal(err)
				}
				if err = s.Complete(a2.Ref, journal.Accepted); err != nil {
					t.Fatal(err)
				}
				m.Append(ctx, u.Down, raw2)
				m.hint = mailbox.Message{Folder: u.Down, Validity: 1, UID: 2, MIME: raw2}
				eligible[a2.Ref] = true
				if mode == "cached-pair-plus-fetched" || mode == "cached-pair-delete-error" {
					l.outCache = append(l.outCache, m.hint)
					body3 := []byte{7, 8, 9}
					raw3, _ := mailbox.Encode(body3)
					a3, err := s.Prepare(g.Ref, u.Down, wire.ID{3}, uint64(len(raw3)), body3)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.Start(a3.Ref); err != nil {
						t.Fatal(err)
					}
					if err = s.Complete(a3.Ref, journal.Accepted); err != nil {
						t.Fatal(err)
					}
					m.Append(ctx, u.Down, raw3)
					m.hint = mailbox.Message{Folder: u.Down, Validity: 1, UID: 3, MIME: raw3}
					eligible[a3.Ref] = true
				}
				if mode == "cached-plus-delete-error" || mode == "cached-pair-delete-error" {
					m.deleteErr = errors.New("provider delete failed")
				}
			}
			before, _ := s.Held(a.ID, u.ID)
			result := l.cleanup(cleanupLocalData, eligible, nil, nil, true, s)
			after, _ := s.Held(a.ID, u.ID)
			if m.calls != 1 {
				t.Fatal("bounded discovery did not run", m.calls)
			}
			if mode == "proved" || mode == "cached-plus-fetched" || mode == "cached-pair-plus-fetched" {
				if result.err != nil || len(result.local) != len(eligible) || after.Messages != before.Messages-uint64(len(eligible)) {
					t.Fatal("verified physical cleanup failed", result.err, result.local, before, after)
				}
				absent, _ := m.memoryMail.Absent(ctx, u.Down, 1, 1)
				if !absent {
					t.Fatal("refund preceded physical deletion")
				}
			} else if len(result.local) != 0 || before != after {
				t.Fatal("hint refunded unproved debt", mode, result.err, result.local, before, after)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
