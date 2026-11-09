package journal

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/pool"
	"itom.local/core/wire"
)

func TestCommitTimelineRemainsObservableDuringBlockedUpdateAndRetainsFailure(t *testing.T) {
	limit := pool.Amount{Bytes: 10000, Messages: 10}
	cfg := Config{Accounts: []Account{{ID: "account1", Provider: "fixture", Limit: limit, Users: []User{{ID: "12345678", Limit: limit, Folders: []string{"fast", "bulk"}}}}}, MaxGrants: 20, MaxAttempts: 20}
	s, err := Open(filepath.Join(t.TempDir(), "issuer.db"), cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory("account1", "12345678", s.Run()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	s.beforeCommit = func() error { close(entered); <-release; return nil }
	type result struct {
		g   Grant
		err error
	}
	done := make(chan result, 1)
	go func() {
		g, err := s.Issue(Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}, limit)
		done <- result{g, err}
	}()
	<-entered
	observed := make(chan CommitTimeline, 1)
	go func() { observed <- s.CommitTimeline() }()
	var held CommitTimeline
	select {
	case held = <-observed:
	case <-time.After(200 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("diagnostics waited on the database lock")
	}
	if held.ActiveSinceNS <= 0 || held.ActiveSinceNS > held.SampledAtNS {
		close(release)
		<-done
		t.Fatal("in-progress durable update invisible", held)
	}
	select {
	case <-done:
		close(release)
		t.Fatal("blocked commit returned permission")
	default:
	}
	close(release)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.beforeCommit = nil
	now := s.CommitTimeline()
	if now.ActiveSinceNS != 0 || len(now.Slowest) == 0 || now.Slowest[0].StartedNS > held.SampledAtNS || now.Slowest[0].FinishedNS < held.SampledAtNS {
		t.Fatal("cannot correlate update with observed pause", held, now)
	}
	now.Slowest[0].StartedNS = -1
	if s.CommitTimeline().Slowest[0].StartedNS < 0 {
		t.Fatal("mutable diagnostic event escaped")
	}
	if err = s.CloseGrant(r.g.Ref); err != nil {
		t.Fatal(err)
	}
	scope := Scope{Account: "account1", User: "12345678", Session: wire.ID{1}}
	for range 20 {
		g, err := s.Issue(scope, limit)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.CloseGrant(g.Ref); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.CommitTimeline().Slowest) != maxCommitEvents {
		t.Fatal("unbounded or missing commit history")
	}
	s.afterCommit = func() error { return errors.New("lost durable acknowledgement") }
	if _, err = s.Issue(scope, limit); err == nil {
		t.Fatal("lost acknowledgement returned success")
	}
	if _, err = s.Issue(scope, limit); !errors.Is(err, ErrPoisoned) {
		t.Fatal("lost acknowledgement did not poison admission", err)
	}
	found := false
	for _, e := range s.CommitTimeline().Slowest {
		found = found || e.Failed
	}
	// A short failed update need not enter the longest-duration set. Its original
	// failure counter remains authoritative for diagnosis, never for permission.
	if !found && s.CommitSnapshot().Failures == 0 {
		t.Fatal("lost acknowledgement hidden from diagnostics")
	}
}
