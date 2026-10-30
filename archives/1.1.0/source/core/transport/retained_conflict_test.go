package transport_test

import (
	"bytes"
	"context"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/transport"
	"testing"
	"time"
)

func TestHistoricalLocatorConflictAndDuplicateCannotAbortDiscoveryOrReleaseDebt(t *testing.T) {
	for _, mode := range []string{"locator_conflict", "duplicate_body"} {
		t.Run(mode, func(t *testing.T) {
			f := serverPublications(t, transport.Fast, 4<<20)
			box := &sharedMailbox{keywords: true, reclamation: true}
			sx, cx := box.selected(t, "fast"), box.selected(t, "fast")
			p := serverBody(t, f, []byte("original retained server publication"))
			raw := p.MIME()
			decoded, err := mailbox.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Start(context.Background(), sx); err != nil {
				t.Fatal(err)
			}
			ref, _ := p.ServerAttempt()
			attempt, err := f.store.GetAttempt(ref)
			if err != nil {
				t.Fatal(err)
			}
			uid := uint32(1)
			if mode == "locator_conflict" {
				uid = 99
			}
			original := journal.Location{UIDValidity: 9, UID: uid}
			if err = f.store.Observe(ref, f.store.Run(), journal.Observation{Account: "account1", Folder: "fast", EncodedBytes: attempt.EncodedBytes, Location: original, Decoded: decoded}); err != nil {
				t.Fatal(err)
			}
			// A genuine receipt is present for UID1. It must not authorize release
			// of an attempt bound elsewhere or represented by multiple physical UIDs.
			ci, err := transport.NewClientInbox(cx, transport.DefaultInboxLimits(), f.incoming)
			if err != nil {
				t.Fatal(err)
			}
			if err = ci.EnableDeliveryReceipts(); err != nil {
				t.Fatal(err)
			}
			if _, err = ci.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			w, err := transport.NewInboxWorker(ci, transport.DefaultInboxServiceLimits())
			if err != nil {
				t.Fatal(err)
			}
			cancel, done := startReclamationReader(t, w)
			awaitReclamation(t, func() bool { return ci.DeliveryReceiptStats().Marked == 1 })
			cancel()
			if err = <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "duplicate_body" {
				box.mu.Lock()
				box.bodies = append(box.bodies, bytes.Clone(raw))
				box.bytes += len(raw)
				box.mu.Unlock()
			}
			held, _ := f.store.Held("account1", "12345678")
			x := box.selected(t, "fast")
			ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			snapshot, err := transport.InspectRetainedAndReclaimReceived(ctx, x, "12345678", f.key, f.store)
			if err != nil || snapshot == nil {
				t.Fatal("optional historical proof conflict aborted startup inventory", mode, err)
			}
			saved, err := f.store.GetAttempt(ref)
			after, _ := f.store.Held("account1", "12345678")
			if err != nil || saved.Location != original || after != held {
				t.Fatal("conflicting history supplied deletion/refund authority", saved.Location, original, held, after, err)
			}
			box.mu.Lock()
			live := box.liveCountLocked()
			box.mu.Unlock()
			expected := 1
			if mode == "duplicate_body" {
				expected = 2
			}
			if live != expected || x.Snapshot().Expunge.Started != 0 {
				t.Fatal("ambiguous physical mail was deleted", live, x.Snapshot())
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOptionalRetainedCleanupFailureCannotInvalidateCompletedAuthenticatedInventory(t *testing.T) {
	for _, mode := range []string{"flags_refused", "lost_flags_tag", "bad_body"} {
		t.Run(mode, func(t *testing.T) {
			f := serverPublications(t, transport.Fast, 4<<20)
			box := &sharedMailbox{keywords: true, reclamation: true}
			sx := box.selected(t, "fast")
			p := serverBody(t, f, []byte("full original BODY remains charged when optional proof fails"))
			if _, err := p.Start(context.Background(), sx); err != nil {
				t.Fatal(err)
			}
			before, _ := f.store.Held("account1", "12345678")
			box.mu.Lock()
			box.reclaimFault = mode
			if mode == "bad_body" {
				box.bodies[0] = []byte("not an authenticated mailbox envelope")
			}
			box.mu.Unlock()
			x := box.selected(t, "fast")
			ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			snapshot, err := transport.InspectRetainedAndReclaimReceived(ctx, x, "12345678", f.key, f.store)
			state := x.Snapshot()
			if mode == "bad_body" {
				if err == nil || snapshot != nil || state.RetainedInventory.Failure == "" || state.RetainedInventory.Stage != "authenticate_body" {
					t.Fatal("incomplete or unauthenticated inventory was enabled", err, state.RetainedInventory)
				}
			} else {
				if err != nil || snapshot == nil || !state.RetainedInventory.CleanupDeferred || state.RetainedInventory.DeferredFailure == "" {
					t.Fatal("failed optional proof prevented planned service selection", err, state)
				}
			}
			after, _ := f.store.Held("account1", "12345678")
			if after != before || x.Snapshot().Expunge.Started != 0 {
				t.Fatal("failed cleanup refunded physical debt", before, after)
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
