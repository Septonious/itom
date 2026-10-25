package transport_test

import (
	"context"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"path/filepath"
	"testing"
	"time"
)

// Each candidate originates in a real wallet commit, APPEND, full authenticated
// FETCH and logical Apply. The unchanged physical fixture holds at most 64 LIVE
// messages, with monotonically increasing UIDs rather than recycled positions.
func TestReclamationBatchesReadyBodiesAtTheOriginalProofBound(t *testing.T) {
	f := publications(t, 64<<20, 64)
	box := &sharedMailbox{reclamation: true}
	x := box.selected(t, "fast")
	for range 49 {
		p := prepare(t, f, []byte("already delivered group"))
		if outcome, err := p.Start(context.Background(), x); err != nil || outcome != transport.Accepted {
			t.Fatal(outcome, err)
		}
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if p, err := inbox.Sweep(context.Background()); err != nil || p.Applied != 49 {
		t.Fatal(p, err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	awaitReclamation(t, func() bool { return inbox.ReclamationStats().Released == 49 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	s := inbox.ReclamationStats()
	commands := x.Snapshot()
	if s.MaxBatch != 32 || s.Batches != 2 || commands.Expunge.Started != 2 || commands.Store.Started != 2 || commands.Closed {
		t.Fatal("ready bodies were serialized per message or exceeded original proof limit", s, commands)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 49 || len(entries[0].Pending) != 49 {
		t.Fatal("batch cleanup fabricated wallet credit", entries, err)
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestPartialBatchAbsenceOnlyReleasesMissingUIDsWithoutRepeatingExpunge(t *testing.T) {
	f := publications(t, 4<<20, 8)
	box := &sharedMailbox{reclamation: true, reclaimFault: "partial_visible"}
	x := box.selected(t, "fast")
	for range 3 {
		p := prepare(t, f, []byte("retained if still visible"))
		if _, err := p.Start(context.Background(), x); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	awaitReclamation(t, func() bool { s := inbox.ReclamationStats(); return s.Released == 2 && s.Probes >= 3 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s := inbox.ReclamationStats(); s.Released != 2 || s.Pending != 1 || s.Batches != 1 || x.Snapshot().Expunge.Started != 1 {
		t.Fatal("presence was treated as absence or repeated a completed mutation", s, x.Snapshot())
	}
	awaitReclamation(t, func() bool { return !x.Snapshot().FetchActive })
	if body := fetchAt(t, x, 2); len(body.Bytes) == 0 {
		t.Fatal("still visible body was lost")
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchReclamationRetainsTheSameExpungeAcrossCancellation(t *testing.T) {
	f := publications(t, 4<<20, 8)
	entered, release := make(chan struct{}), make(chan struct{})
	box := &sharedMailbox{reclamation: true, expungeEntered: entered, expungeRelease: release}
	x := box.selected(t, "fast")
	for range 3 {
		p := prepare(t, f, []byte("one pending exact-set command"))
		if _, err := p.Start(context.Background(), x); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("batch did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatal(err)
	}
	if s := inbox.ReclamationStats(); s.Released != 0 || s.Pending != 3 || x.Snapshot().Expunge.Started != 1 {
		close(release)
		t.Fatal("detached command released debt", s)
	}
	close(release)
	cancel, done = startReclamationReader(t, worker)
	awaitReclamation(t, func() bool { return inbox.ReclamationStats().Released == 3 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s := x.Snapshot(); s.Expunge.Started != 1 || s.Expunge.Completed != 1 || s.Closed {
		t.Fatal("resume repeated the original command or lost the carrier", s)
	}
}

func TestLostBatchExpungeTagRetainsEveryOriginalCharge(t *testing.T) {
	f := publications(t, 4<<20, 8)
	box := &sharedMailbox{reclamation: true, reclaimFault: "lost_expunge_tag"}
	x := box.selected(t, "fast")
	for range 3 {
		p := prepare(t, f, []byte("retain all ambiguous physical debt"))
		if _, err := p.Start(context.Background(), x); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), f.incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	defer cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost batch tag reported success")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ambiguous batch did not stop")
	}
	if s := inbox.ReclamationStats(); s.Released != 0 || s.Pending != 3 {
		t.Fatal("unknown exact-set deletion refunded any member", s)
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 3 || len(entries[0].Pending) != 3 {
		t.Fatal("ambiguous batch changed client history", entries, err)
	}
}

func batchClientRoute(t *testing.T, f *serverFixture, store *journal.Store, class transport.LedgerClass) (*transport.ClientPublisher, *transport.RemoteReceiver) {
	t.Helper()
	w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), store.StoreID(), f.clientEpoch)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	g, err := store.IssueRemote(f.scope, pool.Amount{Bytes: 2 << 20, Messages: 8})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := store.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	pub, err := transport.NewClientPublisher(f.client, transport.Fast, class, f.clientKey, w, "account1", "fast", f.key)
	if err != nil {
		t.Fatal(err)
	}
	recv, err := transport.NewRemoteReceiver(f.server, class, f.serverKey, store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	return pub, recv
}

func publishBatchData(t *testing.T, f *serverFixture, pub *transport.ClientPublisher, x *transport.Executor) {
	t.Helper()
	if err := f.client.Queue(1, []byte("real charged client request"), time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := pub.Prepare(context.Background(), transport.MaxPayload)
	if err != nil || p == nil {
		t.Fatal("physical DATA preparation", err)
	}
	if _, err := p.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
}

func TestSparseCleanupSetCannotExpungeAnInterveningFlaggedServerBody(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	pub, recv := batchClientRoute(t, f, f.store, transport.DataLedger)
	box := &sharedMailbox{reclamation: true, keywords: true}
	x := box.selected(t, "fast")
	publishBatchData(t, f, pub, x)
	own := serverBody(t, f, []byte("not delivered to the client; retain me"))
	if _, err := own.Start(context.Background(), x); err != nil {
		t.Fatal(err)
	}
	publishBatchData(t, f, pub, x)
	// Another mailbox actor's flag must never turn our explicit UID set into
	// a generic expunge or numeric range that deletes the intervening message.
	box.mu.Lock()
	box.flags = map[uint32]map[string]bool{2: {`\Deleted`: true}}
	box.mu.Unlock()
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), recv)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	awaitReclamation(t, func() bool { return inbox.ReclamationStats().Released == 2 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	awaitReclamation(t, func() bool { return !x.Snapshot().FetchActive })
	if body := fetchAt(t, x, 2); len(body.Bytes) == 0 {
		t.Fatal("unacknowledged flagged server body was expunged")
	}
	ref, _ := own.ServerAttempt()
	if _, err := f.store.GetAttempt(ref); err != nil {
		t.Fatal("unproven local debt lost", err)
	}
	if s := inbox.ReclamationStats(); s.Pending != 1 || s.MaxBatch != 2 || x.Snapshot().Expunge.Started != 1 {
		t.Fatal("sparse ready set was expanded or serialized", s, x.Snapshot())
	}
	if err := f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupBatchesKeepDataAndControlIssuersSeparate(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	control := issuer(t, filepath.Join(t.TempDir(), "control.db"), 4<<20, 128)
	dataPub, dataRecv := batchClientRoute(t, f, f.store, transport.DataLedger)
	controlPub, controlRecv := batchClientRoute(t, f, control, transport.ControlLedger)
	box := &sharedMailbox{reclamation: true}
	x := box.selected(t, "fast")
	for n := 1; n <= 3; n++ {
		publishBatchData(t, f, dataPub, x)
		if err := f.client.GrowReceive(1, uint64(1<<20+n*8192)); err != nil {
			t.Fatal(err)
		}
		p, err := controlPub.Prepare(context.Background(), transport.MaxPayload)
		if err != nil || p == nil {
			t.Fatal("backed CONTROL preparation", err)
		}
		if _, err := p.Start(context.Background(), x); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := transport.NewServerInbox(x, transport.DefaultInboxLimits(), dataRecv, controlRecv)
	if err != nil {
		t.Fatal(err)
	}
	if err := inbox.EnableReclamation(); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	worker, err := transport.NewInboxWorker(inbox, transport.DefaultInboxServiceLimits())
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := startReclamationReader(t, worker)
	awaitReclamation(t, func() bool { return inbox.ReclamationStats().Released == 6 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s := inbox.ReclamationStats(); s.Batches != 2 || s.MaxBatch != 3 || x.Snapshot().Expunge.Started != 2 {
		t.Fatal("issuer scopes were mixed or serialized", s, x.Snapshot())
	}
	for _, store := range []*journal.Store{f.store, control} {
		if err := store.Audit(); err != nil {
			t.Fatal(err)
		}
	}
}
