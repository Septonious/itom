package transport_test

import (
	"context"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRestartInventoryReclaimsOnlyOriginalLocallySentReceivedAndAbsentBodies(t *testing.T) {
	for _, mode := range []string{"received", "still_visible", "transplanted_receipt"} {
		t.Run(mode, func(t *testing.T) {
			f := serverPublications(t, transport.Fast, 4<<20)
			box := &sharedMailbox{reclamation: true, keywords: true}
			sx, cx := box.selected(t, "fast"), box.selected(t, "fast")
			one := serverBody(t, f, []byte("received in the old authenticated session"))
			if _, err := one.Start(context.Background(), sx); err != nil {
				t.Fatal(err)
			}
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
			worker, err := transport.NewInboxWorker(ci, transport.DefaultInboxServiceLimits())
			if err != nil {
				t.Fatal(err)
			}
			cancel, done := startReclamationReader(t, worker)
			awaitReclamation(t, func() bool { return ci.DeliveryReceiptStats().Marked == 1 })
			cancel()
			if err = <-done; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			two := serverBody(t, f, []byte("not received; a copied receipt cannot authorize deletion"))
			if _, err = two.Start(context.Background(), sx); err != nil {
				t.Fatal(err)
			}
			unknown, err := f.store.Issue(f.scope, pool.Amount{Bytes: 128, Messages: 1})
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := f.store.PrepareStarted(unknown.Ref, "fast", wire.ID{97}, 128, []byte("unknown physical intent with no observed original body"))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.CompleteAndClose(attempt.Ref, journal.Uncertain); err != nil {
				t.Fatal(err)
			}
			box.mu.Lock()
			// Even an unrelated deletion flag and a real receipt copied from another
			// UID/body must not turn this into generic EXPUNGE or full-inventory refund.
			box.flags[2] = map[string]bool{`\Deleted`: true}
			for flag := range box.flags[1] {
				box.flags[2][flag] = true
			}
			if mode == "still_visible" {
				box.reclaimFault = "still_visible"
			}
			if mode == "transplanted_receipt" {
				box.flags[1] = map[string]bool{}
			}
			box.mu.Unlock()
			before, _ := f.store.Held("account1", "12345678")
			f.client.Close()
			f.server.Close()
			cx.Close()
			sx.Close()
			inventory := box.selected(t, "fast")
			ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			for range 2 {
				if _, err = transport.InspectRetainedAndReclaimReceived(ctx, inventory, "12345678", f.key, f.store); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := f.store.Held("account1", "12345678")
			expected := before.Messages
			if mode == "received" {
				expected--
			}
			if after.Messages != expected {
				t.Fatal("receipt, deletion and independent absence were not all required", before, after)
			}
			saved, err := f.store.GetAttempt(attempt.Ref)
			if err != nil || saved.State != journal.Unknown {
				t.Fatal("inventory refunded unproven historical debt", saved, err)
			}
			ref, _ := two.ServerAttempt()
			if _, err = f.store.GetAttempt(ref); err != nil {
				t.Fatal("copied receipt deleted unreceived body", err)
			}
			box.mu.Lock()
			unreceived := len(box.bodies[1])
			box.mu.Unlock()
			if unreceived == 0 {
				t.Fatal("exact UID set expanded to unrelated flagged mail")
			}
			if err = f.store.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
