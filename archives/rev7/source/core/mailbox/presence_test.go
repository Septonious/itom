package mailbox

import (
	"context"
	"errors"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPresentSparseUIDSetWithDeletedHoles(t *testing.T) {
	cfg, tc := testServer(t, "tls", true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"present"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "present"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		raw, encodeErr := Encode([]byte(fmt.Sprintf("present-%02d", i)))
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if err = c.Append(ctx, "present", raw); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := c.Recent(ctx, "present", 40)
	if err != nil || len(rows) != 40 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	removeAt := []int{1, 6, 11, 16, 21, 26, 31, 36}
	removed := make(map[uint32]bool, len(removeAt))
	toDelete := make([]Message, 0, len(removeAt))
	for _, i := range removeAt {
		toDelete = append(toDelete, rows[i])
		removed[rows[i].UID] = true
	}
	if err = c.DeleteMany(ctx, toDelete); err != nil {
		t.Fatal(err)
	}
	ids := make([]uint32, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UID)
	}
	present, err := c.Present(ctx, "present", rows[0].Validity, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(present) != len(rows)-len(toDelete) {
		t.Fatalf("present=%d want=%d", len(present), len(rows)-len(toDelete))
	}
	for _, id := range ids {
		if present[id] == removed[id] {
			t.Fatalf("uid=%d present=%t removed=%t", id, present[id], removed[id])
		}
	}
}

type presenceRaceFetch struct {
	imapserver.Session
	arm     *atomic.Bool
	started chan<- struct{}
	release <-chan struct{}
}

func (s *presenceRaceFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if opts != nil && opts.UID && len(opts.BodySection) == 0 && s.arm.CompareAndSwap(true, false) {
		close(s.started)
		<-s.release
	}
	return s.Session.Fetch(w, set, opts)
}

func TestPresentConcurrentExpungeRetainsAmbiguousUIDUntilRetry(t *testing.T) {
	var arm atomic.Bool
	started := make(chan struct{})
	release := make(chan struct{})
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		return &presenceRaceFetch{Session: s, arm: &arm, started: started, release: release}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	presence, err := dial(ctx, cfg, []string{"present-race"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer presence.Close()
	deleter, err := dial(ctx, cfg, []string{"present-race"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer deleter.Close()
	if err = presence.Ensure(ctx, "present-race"); err != nil {
		t.Fatal(err)
	}
	if err = deleter.Ensure(ctx, "present-race"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("race"))
	if err = presence.Append(ctx, "present-race", raw); err != nil {
		t.Fatal(err)
	}
	rows, _, err := presence.Recent(ctx, "present-race", 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	msg := rows[0]

	type result struct {
		present map[uint32]bool
		err     error
	}
	resultCh := make(chan result, 1)
	var releaseOnce sync.Once
	releaseFetch := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseFetch()
	arm.Store(true)
	go func() {
		found, e := presence.Present(ctx, "present-race", msg.Validity, []uint32{msg.UID})
		resultCh <- result{present: found, err: e}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("presence FETCH did not reach race point", ctx.Err())
	}
	// STORE +FLAGS.SILENT queues a UID+FLAGS update for the other selected
	// session, then EXPUNGE removes the message before its blocked UID FETCH.
	// go-imap may route that queued update into the pending UID FETCH. It is
	// conservative evidence of concurrent presence/change, not a scope error.
	deleteErr := deleter.Delete(ctx, msg)
	releaseFetch()
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	var first result
	select {
	case first = <-resultCh:
	case <-ctx.Done():
		t.Fatal("presence race did not finish", ctx.Err())
	}
	if first.err != nil {
		t.Fatal(first.err)
	}
	if !first.present[msg.UID] {
		t.Fatal("concurrent UID+FLAGS update was treated as proven absence")
	}
	// The conservative result is temporary. A clean subsequent proof must see
	// that the UID is now absent, so GC can prune the locator on a later pass.
	again, err := presence.Present(ctx, "present-race", msg.Validity, []uint32{msg.UID})
	if err != nil {
		t.Fatal(err)
	}
	if again[msg.UID] {
		t.Fatal("stale concurrent presence survived a clean retry")
	}
}

type missingSizeFetch struct{ imapserver.Session }

func (s missingSizeFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	m := w.CreateMessage(1)
	m.WriteUID(1)
	return m.Close()
}

func TestFetchScopeDetailPreservesHighLevelClassification(t *testing.T) {
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session { return missingSizeFetch{s} })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"scope-detail"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "scope-detail"); err != nil {
		t.Fatal(err)
	}
	raw, _ := Encode([]byte("scope"))
	if err = c.Append(ctx, "scope-detail", raw); err != nil {
		t.Fatal(err)
	}
	_, _, err = c.Page(ctx, "scope-detail", Cursor{})
	if !errors.Is(err, ErrScope) {
		t.Fatalf("missing high-level IMAP_SCOPE classification: %v", err)
	}
	if detail := ScopeDetail(err); detail != "IMAP_SCOPE_FETCH_PARTIAL_RETRY_EXHAUSTED" {
		t.Fatalf("scope detail=%q", detail)
	}
}

type uidOnlyUpdateBeforeFullFetch struct {
	imapserver.Session
	arm atomic.Bool
}

func (s *uidOnlyUpdateBeforeFullFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if opts != nil && opts.UID && opts.RFC822Size && len(opts.BodySection) > 0 && s.arm.CompareAndSwap(true, false) {
		m := w.CreateMessage(1)
		m.WriteUID(1)
		if err := m.Close(); err != nil {
			return err
		}
	}
	return s.Session.Fetch(w, set, opts)
}

func TestPageRetriesUIDOnlyUpdateBeforeFullRow(t *testing.T) {
	var wrapped *uidOnlyUpdateBeforeFullFetch
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		wrapped = &uidOnlyUpdateBeforeFullFetch{Session: s}
		return wrapped
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"fetch-uid-only-retry"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "fetch-uid-only-retry"); err != nil {
		t.Fatal(err)
	}
	raw, err := Encode([]byte("uid-only-update-must-not-poison-page"))
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Append(ctx, "fetch-uid-only-retry", raw); err != nil {
		t.Fatal(err)
	}
	wrapped.arm.Store(true)
	rows, next, err := c.Page(ctx, "fetch-uid-only-retry", Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UID != 1 || next.After != 1 {
		t.Fatalf("rows=%d next=%+v", len(rows), next)
	}
}

type partialUpdateBeforeFullFetch struct {
	imapserver.Session
	arm atomic.Bool
}

func (s *partialUpdateBeforeFullFetch) Fetch(w *imapserver.FetchWriter, set imap.NumSet, opts *imap.FetchOptions) error {
	if opts != nil && opts.UID && opts.RFC822Size && len(opts.BodySection) > 0 && s.arm.CompareAndSwap(true, false) {
		// Simulate the shape go-imap can attach to a pending UID FETCH when a
		// concurrent STORE queues a unilateral flag update first. The partial
		// row claims UID 1 inside the command; the real full row emitted by the
		// delegated Fetch is therefore routed as unilateral data by the client.
		m := w.CreateMessage(1)
		m.WriteUID(1)
		m.WriteFlags([]imap.Flag{imap.FlagSeen})
		if err := m.Close(); err != nil {
			return err
		}
	}
	return s.Session.Fetch(w, set, opts)
}

func TestPageRetainsFullBodyAfterUnsolicitedPartialFetch(t *testing.T) {
	var wrapped *partialUpdateBeforeFullFetch
	cfg, tc := testServerWithSession(t, "tls", true, func(s imapserver.Session) imapserver.Session {
		wrapped = &partialUpdateBeforeFullFetch{Session: s}
		return wrapped
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := dial(ctx, cfg, []string{"fetch-retry"}, tc)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.Ensure(ctx, "fetch-retry"); err != nil {
		t.Fatal(err)
	}
	raw, err := Encode([]byte("full-row-must-survive"))
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Append(ctx, "fetch-retry", raw); err != nil {
		t.Fatal(err)
	}
	wrapped.arm.Store(true)
	rows, next, err := c.Page(ctx, "fetch-retry", Cursor{})
	if c.partialRetries.Load() != 0 {
		t.Fatal("partial update stole the solicited BODY")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UID != 1 {
		t.Fatalf("rows=%d uid=%v", len(rows), func() uint32 {
			if len(rows) == 0 {
				return 0
			}
			return rows[0].UID
		}())
	}
	if next.After != 1 || next.Validity == 0 {
		t.Fatalf("next=%+v", next)
	}
}
