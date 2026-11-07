package mailstream

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type byteCollectionFixture struct {
	mu          sync.Mutex
	sizes       map[string]map[imap.UID]int64
	deleted     map[string]map[imap.UID]bool
	oversized   int
	inventories int
	selections  int
	uidProofs   int
	omitExpunge bool
	limit       int64
	delay       time.Duration
}
type byteCollectionSession struct {
	imapserver.Session
	fixture        *byteCollectionFixture
	user, selected string
}

func (s *byteCollectionSession) Login(u, p string) error { s.user = u; return s.Session.Login(u, p) }
func (s *byteCollectionSession) Select(n string, o *imap.SelectOptions) (*imap.SelectData, error) {
	d, e := s.Session.Select(n, o)
	if e == nil {
		s.selected = n
		s.fixture.mu.Lock()
		s.fixture.selections++
		s.fixture.mu.Unlock()
	}
	return d, e
}
func (s *byteCollectionSession) Append(n string, r imap.LiteralReader, o *imap.AppendOptions) (*imap.AppendData, error) {
	size := r.Size()
	d, e := s.Session.Append(n, r, o)
	if e == nil {
		s.fixture.mu.Lock()
		key := s.user + "/" + n
		if s.fixture.sizes[key] == nil {
			s.fixture.sizes[key] = map[imap.UID]int64{}
		}
		s.fixture.sizes[key][d.UID] = size
		s.fixture.mu.Unlock()
	}
	return d, e
}
func (s *byteCollectionSession) Store(w *imapserver.FetchWriter, set imap.NumSet, f *imap.StoreFlags, o *imap.StoreOptions) error {
	e := s.Session.Store(w, set, f, o)
	if e != nil {
		return e
	}
	for _, flag := range f.Flags {
		if flag != imap.FlagDeleted {
			continue
		}
		uids, ok := set.(imap.UIDSet)
		if !ok {
			continue
		}
		nums, ok := uids.Nums()
		if !ok {
			continue
		}
		s.fixture.mu.Lock()
		key := s.user + "/" + s.selected
		if s.fixture.deleted[key] == nil {
			s.fixture.deleted[key] = map[imap.UID]bool{}
		}
		for _, uid := range nums {
			if f.Op == imap.StoreFlagsAdd {
				s.fixture.deleted[key][uid] = true
			} else if f.Op == imap.StoreFlagsDel {
				delete(s.fixture.deleted[key], uid)
			}
		}
		s.fixture.mu.Unlock()
	}
	return nil
}
func (s *byteCollectionSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	s.fixture.mu.Lock()
	if o.RFC822Size {
		s.fixture.inventories++
	} else if o.UID {
		s.fixture.uidProofs++
	}
	s.fixture.mu.Unlock()
	return s.Session.Fetch(w, set, o)
}

func (s *byteCollectionSession) Expunge(w *imapserver.ExpungeWriter, u *imap.UIDSet) error {
	s.fixture.mu.Lock()
	key := s.user + "/" + s.selected
	var size int64
	for uid := range s.fixture.deleted[key] {
		size += s.fixture.sizes[key][uid]
	}
	limit := s.fixture.limit
	if limit == 0 {
		limit = 4 << 20
	}
	if size > limit {
		s.fixture.oversized++
		s.fixture.mu.Unlock()
		return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "fixture deletion byte budget exceeded"}
	}
	s.fixture.mu.Unlock()
	if s.fixture.delay > 0 {
		time.Sleep(s.fixture.delay)
	}
	if s.fixture.omitExpunge {
		return nil
	}
	e := s.Session.Expunge(w, u)
	if e == nil {
		s.fixture.mu.Lock()
		clear(s.fixture.deleted[key])
		s.fixture.mu.Unlock()
	}
	return e
}

func seedSizedCollection(t *testing.T, p *mailPipe, folder string, size int) *segmentRecord {
	t.Helper()
	if e := p.client.Create(folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	rec := &segmentRecord{folder: folder, queued: true, retired: true, appends: 128}
	raw := bytes.Repeat([]byte{'x'}, size)
	for range 128 {
		cmd, e := p.startAppend(folder, raw)
		if e != nil {
			t.Fatal(e)
		}
		d, e := cmd.Wait()
		if e != nil {
			t.Fatal(e)
		}
		rec.uids.AddNum(d.UID)
		rec.validity = d.UIDValidity
		rec.bytes += len(raw)
	}
	return rec
}

// A previously timed-out full deletion may have left every owned UID flagged.
// Each turn must stay below the provider's byte budget, retain all debt during
// partial progress, keep the carrier, then refund only after the empty audit.
func TestByteBoundedDataCollectionRetainsDebtUntilFinalAudit(t *testing.T) {
	f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if p != nil {
			p.close()
		}
	}()
	rec := seedSizedCollection(t, p, "itom3-byte-page", 128<<10)
	if _, e = p.client.Select(rec.folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	if e = p.client.Store(rec.uids, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); e != nil {
		t.Fatal(e)
	}
	c := NewReclamationCoordinator()
	l := &IMAPLink{ctx: ctx, reclaimer: c, changed: make(chan struct{})}
	key := segmentKey{session: SessionID{1}}
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], writer: p, segments: map[segmentKey]*segmentRecord{key: rec}, spent: rec.bytes, reserved: rec.bytes}
	originalDebt := a.spent
	turns := 0
	var joined sync.WaitGroup
	inFlight := 0
	// The normal complete-generation attempt is refused once. Its exact
	// ownership/debt survives, and retries must use bounded pages thereafter.
	a.processCleanup(cleanupJob{account: a, key: key, record: rec}, &p, make(chan error, 1), &inFlight, &joined)
	if !rec.refused || !c.snapshot().Paused || a.spent != originalDebt || p == nil {
		t.Fatalf("initial refusal did not preserve owned debt and retry state: %+v", c.snapshot())
	}
	for !c.permitted(a, rec.folder) {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	original := p
	for a.spent > 0 && turns < 10 {
		turns++
		rec.queued = true
		a.processCleanup(cleanupJob{account: a, key: key, record: rec}, &p, make(chan error, 1), &inFlight, &joined)
		if p != original || c.snapshot().Paused {
			t.Fatalf("bounded DATA collection failed: same_writer=%v state=%+v", p == original, c.snapshot())
		}
		if a.spent > 0 && (a.spent != originalDebt || a.retired.Load() != 0) {
			t.Fatalf("partial collection refunded unaudited debt: remaining=%d retired=%d", a.spent, a.retired.Load())
		}
	}
	if turns < 2 || a.spent != 0 || a.retired.Load() != 1 || f.oversized != 1 {
		t.Fatalf("bounded DATA collection did not finish safely: turns=%d debt=%d retired=%d oversize=%d", turns, a.spent, a.retired.Load(), f.oversized)
	}
	t.Logf("%d bounded collection turns; debt retained until empty-folder audit", turns)
}

func TestDataCollectionResumesAfterPartialUnknownOutcome(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	rec := seedSizedCollection(t, p, "itom3-partial-result", 1024)
	if _, e = p.client.Select(rec.folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	uids, _ := rec.uids.Nums()
	var partial imap.UIDSet
	for _, uid := range uids[:32] {
		partial.AddNum(uid)
	}
	if _, e = p.deleteUIDs(partial, rec.folder); e != nil {
		t.Fatal(e)
	}
	if e = p.reclaim(cleanupJob{record: rec}, NewReclamationCoordinator()); e != nil {
		t.Fatalf("partial DATA outcome cannot resume: %v", e)
	}
	if p.client.Mailbox().NumMessages != 0 {
		t.Fatal("resumed collection lacked empty audit")
	}
}

func TestDataCollectionRejectsUnprovedUIDInPartialFolder(t *testing.T) {
	accounts, _ := applicationMailServer(t, true, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	rec := seedSizedCollection(t, p, "itom3-unproved-result", 1024)
	if _, e = p.client.Select(rec.folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	uids, _ := rec.uids.Nums()
	var partial imap.UIDSet
	for _, uid := range uids[:32] {
		partial.AddNum(uid)
	}
	if _, e = p.deleteUIDs(partial, rec.folder); e != nil {
		t.Fatal(e)
	}
	cmd, e := p.startAppend(rec.folder, []byte("unproved message must remain"))
	if e != nil {
		t.Fatal(e)
	}
	foreign, e := cmd.Wait()
	if e != nil {
		t.Fatal(e)
	}
	if e = p.reclaim(cleanupJob{record: rec}, NewReclamationCoordinator()); e != ErrProtocol {
		t.Fatalf("unproved UID accepted: %v", e)
	}
	rows, e := p.client.Fetch(imap.UIDSetNum(foreign.UID), &imap.FetchOptions{UID: true}).Collect()
	if e != nil || len(rows) != 1 {
		t.Fatal("unproved message was removed", e)
	}
}

func TestHistoricalCollectionPagesRespectConsumedUIDFence(t *testing.T) {
	f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	rec := seedSizedCollection(t, p, "itom3-history-byte-page", 128<<10)
	if _, e = p.client.Select(rec.folder, nil).Wait(); e != nil {
		t.Fatal(e)
	}
	nums, _ := rec.uids.Nums()
	target := uidWatermark{Validity: rec.validity, UID: uint32(nums[63])}
	var eligible imap.UIDSet
	for _, uid := range nums[:64] {
		eligible.AddNum(uid)
	}
	if e = p.client.Store(eligible, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); e != nil {
		t.Fatal(e)
	}
	c := NewReclamationCoordinator()
	proof, n, complete, e := p.collectPrefix(c, rec.folder, target, false)
	if e != nil || complete || n == 0 || n >= 64 || proof.UID >= target.UID {
		t.Fatalf("historical DATA ignored byte page or consumed fence: n=%d complete=%v err=%v", n, complete, e)
	}
	for !complete {
		proof, n, complete, e = p.collectPrefix(c, rec.folder, target, false)
		if e != nil {
			t.Fatal(e)
		}
	}
	if proof != target || p.client.Mailbox().NumMessages != 64 || f.oversized != 0 {
		t.Fatalf("historical collection crossed live UID fence: proof=%+v remaining=%d", proof, p.client.Mailbox().NumMessages)
	}
	rows, e := p.client.Fetch(imap.UIDSet{{Start: nums[64], Stop: nums[127]}}, &imap.FetchOptions{UID: true}).Collect()
	if e != nil || len(rows) != 64 {
		t.Fatal("unconsumed tail changed", e)
	}
}

// Complete incoming proofs intentionally have no byte total. Neither zero nor
// 128 messages proves that an initial EXPUNGE is small. Both directions must
// page the first large generation, retain debt, then obtain an empty audit.
func TestLargeFirstCollectionBoundedForBothDirections(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "incoming"}[unknown], func(t *testing.T) {
			f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}, limit: 16 << 20}
			accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			p, e := openMailPipe(ctx, accounts[0])
			if e != nil {
				t.Fatal(e)
			}
			defer p.close()
			rec := seedSizedCollection(t, p, "itom-first-large-page", 256<<10)
			if unknown {
				rec.remote = true
				rec.bytes = 0
			}
			if e = p.reclaim(cleanupJob{record: rec}, NewReclamationCoordinator()); e == nil || e.Error() != "MAILSTREAM_COLLECTION_PARTIAL" {
				t.Fatalf("first large generation not bounded: %v", e)
			}
			if f.oversized != 0 || p.client.Mailbox().NumMessages == 0 || p.client.Mailbox().NumMessages >= 128 {
				t.Fatal("unsafe first mutation", f.oversized, p.client.Mailbox())
			}
			if e = p.reclaim(cleanupJob{record: rec}, NewReclamationCoordinator()); e != nil {
				t.Fatal("bounded continuation", e)
			}
			if p.client.Mailbox().NumMessages != 0 || f.oversized != 0 {
				t.Fatal("final empty audit missing")
			}
		})
	}
}

// Slow successful pages yield before the hard deadline, keeping the writer and
// all debt. A subsequent bounded turn must complete the audit and refund once.
func TestSlowFirstCollectionYieldsBeforeDeadlineWithoutQuarantine(t *testing.T) {
	f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}, limit: 16 << 20, delay: 2 * time.Second}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if p != nil {
			p.close()
		}
	}()
	rec := seedSizedCollection(t, p, "itom-slow-first-pages", 384<<10)
	c := NewReclamationCoordinator()
	l := &IMAPLink{ctx: ctx, reclaimer: c, changed: make(chan struct{})}
	key := segmentKey{session: SessionID{1}}
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], writer: p, segments: map[segmentKey]*segmentRecord{key: rec}, spent: rec.bytes, reserved: rec.bytes}
	originalDebt := a.spent
	original := p
	var joined sync.WaitGroup
	inFlight := 0
	a.processCleanup(cleanupJob{account: a, key: key, record: rec}, &p, make(chan error, 1), &inFlight, &joined)
	if c.snapshot().Paused || p != original || a.spent != originalDebt || rec.refused || p.client.Mailbox().NumMessages == 0 {
		t.Fatal("slow partial work did not yield conservatively", c.snapshot(), a.spent)
	}
	rec.queued = true
	a.processCleanup(cleanupJob{account: a, key: key, record: rec}, &p, make(chan error, 1), &inFlight, &joined)
	if c.snapshot().Paused || p != original || a.spent != 0 || a.retired.Load() != 1 {
		t.Fatal("safe continuation did not audit/refund", c.snapshot(), a.spent)
	}
}

// A multi-page successful turn reuses only its bounded, immutable generation
// inventory; a later turn must fetch a new one. No storage is credited early.
func TestLargeGenerationUsesOneInventoryPerTurn(t *testing.T) {
	f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}, limit: 16 << 20}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if p != nil {
			p.close()
		}
	}()
	rec := seedSizedCollection(t, p, "itom-inventory-turn", 256<<10)
	c := NewReclamationCoordinator()
	l := &IMAPLink{ctx: ctx, reclaimer: c, changed: make(chan struct{})}
	key := segmentKey{session: SessionID{1}}
	a := &mailAccountState{ctx: ctx, link: l, config: accounts[0], writer: p, segments: map[segmentKey]*segmentRecord{key: rec}, spent: rec.bytes, reserved: rec.bytes}
	// Noncritical control debt must not consume a turn that can refund 32 MiB.
	a.maintenance.requested = true
	a.maintenance.controlBytes = 512 << 10
	a.spent += 512 << 10
	a.reserved += 512 << 10
	var joined sync.WaitGroup
	inFlight := 0
	a.processCleanup(cleanupJob{account: a, key: key, record: rec}, &p, make(chan error, 1), &inFlight, &joined)
	if c.snapshot().Paused || a.spent != 512<<10 || len(a.segments) != 0 || p.client.Mailbox().NumMessages != 0 {
		t.Fatal("complete audited turn did not release charge", a.spent, c.snapshot())
	}
	if f.inventories != 1 || f.oversized != 0 || f.selections != 2 || f.uidProofs != 1 {
		t.Fatal("generation inventory or final count audit repeated, or mutation oversized", f.inventories, f.selections, f.uidProofs, f.oversized)
	}
}

// A tagged OK without actual deletion must not permit the next byte page.
// Otherwise flags left by one attempt could enlarge a later EXPUNGE.
func TestCollectionProvesPageAbsentBeforeFlaggingAnother(t *testing.T) {
	f := &byteCollectionFixture{sizes: map[string]map[imap.UID]int64{}, deleted: map[string]map[imap.UID]bool{}, limit: 16 << 20, omitExpunge: true}
	accounts, _ := applicationMailServerWrapped(t, true, false, func(s imapserver.Session) imapserver.Session { return &byteCollectionSession{Session: s, fixture: f} })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, e := openMailPipe(ctx, accounts[0])
	if e != nil {
		t.Fatal(e)
	}
	defer p.close()
	rec := seedSizedCollection(t, p, "itom-unproven-page", 256<<10)
	if e = p.reclaim(cleanupJob{record: rec}, NewReclamationCoordinator(), collectionTimeout); e != ErrProtocol {
		t.Fatal("accepted unproved page", e)
	}
	if f.oversized != 0 || f.uidProofs != 1 || p.client.Mailbox().NumMessages != segmentAppends {
		t.Fatal("continued or credited an unproved page", f.oversized, f.uidProofs, p.client.Mailbox())
	}
}
