//go:build itomtest

package transport_test

import (
	"bytes"
	"context"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/transport"
)

func TestTenServerWritersDoNotFsyncUnusedGrantsForOneTLSFlight(t *testing.T) {
	const plane = transport.Fast
	f := serverPublications(t, plane, 64<<20)
	folder := "fast"
	var stores []*journal.Store
	var pubs []*transport.ServerPublisher
	for range 10 {
		store := issuer(t, filepath.Join(t.TempDir(), "data.db"), 64<<20, 128)
		// Deterministic bounded metadata service, still followed by REAL fsync.
		// The hook exists only in explicitly tagged synthetic builds.
		store.AddCommitDelayForTest(100 * time.Millisecond)
		p, err := transport.NewServerPublisher(f.server, plane, transport.DataLedger, f.serverKey, store, f.scope, folder, f.key)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, store)
		pubs = append(pubs, p)
	}
	payload := []byte("one TLS flight")
	if err := f.server.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	prepared := make(chan *transport.PreparedPublication, 10)
	failed := make(chan error, 10)
	for _, pub := range pubs {
		workers.Go(func() {
			<-start
			p, err := pub.Prepare(context.Background(), transport.MaxPayload)
			if err != nil {
				failed <- err
			}
			if p != nil {
				prepared <- p
			}
		})
	}
	close(start)
	workers.Wait()
	close(prepared)
	close(failed)
	for err := range failed {
		t.Fatal(err)
	}
	if len(prepared) != 1 {
		t.Fatal("one flight obtained multiple publications", len(prepared))
	}
	var commits uint64
	for _, s := range stores {
		commits += s.CommitSnapshot().Count
		if err := s.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("plane=%d prepared=%d journal_updates=%d", plane, len(prepared), commits)
	if commits != 2 {
		t.Fatal("losing writers reserved/canceled durable grants", commits)
	}
	if snapshot := f.server.Snapshot(); snapshot.FastPreparations != 0 || snapshot.PreparationContention == 0 {
		t.Fatal("preparation intent leaked or herd never exercised", snapshot)
	}
	// This is only a metadata reduction regression. Unknown prepared physical
	// debt is retained; no command, refund or useful-delivery claim is fabricated.
}

func TestFastPreparationCoordinationKeepsBulkJournalPreparationConcurrent(t *testing.T) {
	t.Run("large_backlog", func(t *testing.T) { testBulkPreparationConcurrency(t, 10, 1<<20, 64<<20, 10) })
	t.Run("partial_account_allowance", func(t *testing.T) { testBulkPreparationConcurrency(t, 1, 256<<10, 64<<10, 2) })
}
func testBulkPreparationConcurrency(t *testing.T, flows, payload int, allowance uint64, minimumPrepared int) {
	f := serverPublications(t, transport.Bulk, 64<<20)
	for index := range flows {
		id := uint64(2*index + 1)
		if index > 0 {
			if err := f.server.Add(id, 8<<20, 1<<20); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.server.Queue(id, bytes.Repeat([]byte{1}, payload), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var stores []*journal.Store
	var pubs []*transport.ServerPublisher
	for range 10 {
		s := issuer(t, filepath.Join(t.TempDir(), "data.db"), allowance, 128)
		s.AddCommitDelayForTest(100 * time.Millisecond)
		p, err := transport.NewServerPublisher(f.server, transport.Bulk, transport.DataLedger, f.serverKey, s, f.scope, "bulk", f.key)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, s)
		pubs = append(pubs, p)
	}
	begin := make(chan struct{})
	var workers sync.WaitGroup
	failed := make(chan error, 10)
	prepared := make(chan *transport.PreparedPublication, 10)
	for _, p := range pubs {
		workers.Go(func() {
			<-begin
			body, err := p.PrepareBulk(context.Background(), transport.MaxBulkBytes)
			if err != nil {
				failed <- err
			}
			if body != nil {
				prepared <- body
			}
		})
	}
	close(begin)
	workers.Wait()
	close(failed)
	for err := range failed {
		t.Fatal(err)
	}
	if len(prepared) < minimumPrepared {
		t.Fatal("bulk publication capacity was serialized or stranded", len(prepared))
	}
	type edge struct {
		at    int64
		delta int
	}
	var events []edge
	for _, s := range stores {
		for _, e := range s.CommitTimeline().Slowest {
			if e.Sequence == 1 {
				events = append(events, edge{e.StartedNS, 1}, edge{e.FinishedNS, -1})
			}
		}
		if err := s.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].at < events[j].at })
	live, peak := 0, 0
	for _, e := range events {
		live += e.delta
		peak = max(peak, live)
	}
	t.Logf("bulk prepared=%d overlapping_journal_updates=%d", len(prepared), peak)
	if peak < 2 {
		t.Fatal("fast coordination serialized independent bulk metadata", peak)
	}
}
