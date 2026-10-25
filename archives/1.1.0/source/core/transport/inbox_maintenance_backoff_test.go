package transport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestIdleUnreceiptedMailCannotDriveContinuousFlagsProbing(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	box := &sharedMailbox{reclamation: true, keywords: true}
	x := box.selected(t, "fast")
	p := serverBody(t, f, []byte("delivered to storage, not yet authenticated by client"))
	if outcome, err := p.Start(context.Background(), x); err != nil || outcome != transport.Accepted {
		t.Fatal(outcome, err)
	}
	remote, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), remote)
	if err != nil {
		t.Fatal(err)
	}
	if err = inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err = inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	held, err := f.store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	// A complete accounted SERVER body exists, but no peer MAC can authorize
	// deletion. Measure real parser traffic in a fixed quiet interval, rather
	// than changing the original MinProbe/MaxProbe configuration or deadlines.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err = worker.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	stats := inbox.ReclamationStats()
	if stats.ReceiptProbes < 2 || stats.ReceiptProbes > 20 {
		t.Fatal("quiet retained debt caused a hot metadata loop or no probes", stats)
	}
	if stats.Released != 0 || stats.Batches != 0 {
		t.Fatal("backoff fabricated peer delivery or deletion proof", stats)
	}
	if now, err := f.store.Held("account1", "12345678"); err != nil || now != held {
		t.Fatal("idle retained debt changed", now, err)
	}
	t.Logf("quiet_interval_ms=200 actual_receipt_probes=%d original_max_probe_ms=25", stats.ReceiptProbes)
}
