package wallet

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"itom.local/core/wire"
)

func TestWalletDiagnosticsObserveBlockedDurableUpdateWithoutAuthorizingIt(t *testing.T) {
	w, err := Open(filepath.Join(t.TempDir(), "wallet.db"), "12345678", wire.ID{1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var authorized atomic.Bool
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- w.writeAfter(func(*bolt.Tx) error { close(entered); <-release; return nil }, func() { authorized.Store(true) })
	}()
	defer func() { releaseOnce.Do(func() { close(release) }); <-done }()
	<-entered
	diagnostic := make(chan CommitTimeline, 1)
	go func() { diagnostic <- w.CommitTimeline() }()
	var blocked CommitTimeline
	select {
	case blocked = <-diagnostic:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("wallet diagnostics waited for its durable update")
	}
	if blocked.ActiveSinceNS <= 0 || blocked.ActiveSinceNS > blocked.SampledAtNS || authorized.Load() {
		t.Fatal("blocked update hidden or returned writer permission", blocked)
	}
	releaseOnce.Do(func() { close(release) })
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if !authorized.Load() {
		t.Fatal("committed original update did not finish callback")
	}
	observed := w.CommitTimeline()
	if observed.ActiveSinceNS != 0 || len(observed.Slowest) != 1 || observed.Slowest[0].StartedNS > blocked.SampledAtNS || observed.Slowest[0].FinishedNS < blocked.SampledAtNS {
		t.Fatal("wallet update cannot be correlated with pause", blocked, observed)
	}
	observed.Slowest[0].StartedNS = -1
	if w.CommitTimeline().Slowest[0].StartedNS < 0 {
		t.Fatal("wallet diagnostic event is mutable")
	}
	for range 20 {
		if err = w.write(func(*bolt.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.CommitTimeline().Slowest) != 8 {
		t.Fatal("wallet diagnostic retention is not bounded")
	}
	failure := errors.New("unexpected durable update failure")
	if err = w.write(func(*bolt.Tx) error { return failure }); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err = w.write(func(*bolt.Tx) error { return nil }); !errors.Is(err, ErrIO) {
		t.Fatal("diagnostics changed failed-closed writer admission", err)
	}
	if s := w.CommitSnapshot(); s.Count != 22 || s.Failures != 1 {
		t.Fatal("failed update hidden or blocked write treated as committed", s)
	}
}
