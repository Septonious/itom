package mailstream

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// Fast turnover must not splice three different owners into one diagnostic
// snapshot. The coordinator admits only two simultaneous borrowed writers.
func TestHousekeepingSnapshotUsesOneOwnershipInstant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := &IMAPLink{ctx: ctx, reclaimer: NewReclamationCoordinator(), changed: make(chan struct{})}
	for i := 0; i < 10; i++ {
		l.accounts = append(l.accounts, &mailAccountState{ctx: ctx, link: l, config: MailAccount{ID: fmt.Sprint(i)}})
	}
	var workers sync.WaitGroup
	for start := 0; start < 2; start++ {
		workers.Add(1)
		go func(start int) {
			defer workers.Done()
			for i := start; ctx.Err() == nil; i = (i + 2) % len(l.accounts) {
				a := l.accounts[i]
				if a.borrowWriterForCollection() {
					runtime.Gosched()
					a.returnCollectionWriter()
				}
				runtime.Gosched()
			}
		}(start)
	}
	defer func() { cancel(); workers.Wait() }()
	seen := false
	for range 10000 {
		snapshot := l.Snapshot()
		owners := 0
		for _, line := range snapshot.Lines {
			if line.Housekeeping {
				owners++
			}
		}
		seen = seen || owners > 0
		if owners > 2 {
			t.Fatalf("snapshot reported %d borrowed writers despite the two-writer lease limit", owners)
		}
	}
	if !seen {
		t.Fatal("no concurrent housekeeping was exercised")
	}
}
