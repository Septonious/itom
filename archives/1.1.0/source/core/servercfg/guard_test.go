package servercfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func guardFixture(t *testing.T) (string, User) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.db")
	c, e := New("guard-test")
	if e != nil {
		t.Fatal(e)
	}
	u, e := c.AddUser("admin")
	if e != nil {
		t.Fatal(e)
	}
	if e = c.SetNotice("original"); e != nil {
		t.Fatal(e)
	}
	if e = Update(path, func(dst *Config) error { *dst = c; return nil }); e != nil {
		t.Fatal(e)
	}
	return path, u
}
func beginNotice(t *testing.T, path string, u User, now time.Time) Guard {
	t.Helper()
	g, f, e := BeginGuard(path, u, strings.Repeat("a", 32), []byte(`{"operation":"global_message","text":"candidate"}`), now)
	if e != nil || !f {
		t.Fatal(g, f, e)
	}
	if e = ApplyGuard(path, g.ID, now, func(c *Config) error { return c.SetNotice("candidate") }); e != nil {
		t.Fatal(e)
	}
	if e = CompleteGuardAction(path, g.ID, now); e != nil {
		t.Fatal(e)
	}
	g, e = GuardStatus(path)
	if e != nil {
		t.Fatal(e)
	}
	return g
}

func TestGuardNonceReplayCannotDelayExactLossRollback(t *testing.T) {
	path, u := guardFixture(t)
	now := time.Unix(1000, 0)
	g := beginNotice(t, path, u, now)
	if e := Update(path, func(c *Config) error { c.Provider = "concurrent"; return nil }); !errors.Is(e, ErrGuardPending) {
		t.Fatalf("concurrent CLI edit was allowed: %v", e)
	}
	originalEcho := g.Challenge
	g, e := BeatGuard(path, u.ID, g.ID, g.Challenge, now.Add(10*time.Second))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = BeatGuard(path, u.ID, g.ID, originalEcho, now.Add(11*time.Second)); !errors.Is(e, ErrGuardConflict) {
		t.Fatal("replayed echo renewed connection", e)
	}
	after, _ := GuardStatus(path)
	if after.LastBeatMS != g.LastBeatMS {
		t.Fatal("replay changed deadline")
	}
	if g.Expired(now.Add(69*time.Second)) || !g.Expired(now.Add(70*time.Second)) {
		t.Fatal("loss deadline is not sixty seconds after last verified return")
	}
	before, _ := Load(path)
	if e = MarkGuardRollback(path, g.ID); e != nil {
		t.Fatal(e)
	}
	if e = RollbackGuard(path, g.ID); e != nil {
		t.Fatal(e)
	}
	c, e := Load(path)
	if e != nil || c.Notice.Text != "original" || c.Notice.Revision <= before.Notice.Revision {
		t.Fatalf("rollback did not restore monotonic notice: %#v %v", c.Notice, e)
	}
	done, _ := GuardStatus(path)
	if done.Active() || len(done.Before) != 0 || len(done.Plan) != 0 {
		t.Fatal("finished transaction retained secrets or lock")
	}
	if e = Update(path, func(c *Config) error { c.Provider = "after-rollback"; return nil }); e != nil {
		t.Fatal(e)
	}
	_, fresh, e := BeginGuard(path, u, g.ID, []byte(`{"operation":"global_message","text":"candidate"}`), now.Add(time.Minute))
	if e != nil || fresh {
		t.Fatal("completed request executed again", fresh, e)
	}
}
func TestGuardHealthyEchoesCommitWithoutResettingPhysicalState(t *testing.T) {
	path, u := guardFixture(t)
	now := time.Unix(2000, 0)
	g := beginNotice(t, path, u, now)
	sentinel := filepath.Join(filepath.Dir(path), "physical-journal-sentinel")
	want := []byte("unknown APPEND debt must remain")
	if e := os.WriteFile(sentinel, want, 0600); e != nil {
		t.Fatal(e)
	}
	for second := 5; second <= 65; second += 5 {
		var e error
		g, e = BeatGuard(path, u.ID, g.ID, g.Challenge, now.Add(time.Duration(second)*time.Second))
		if e != nil {
			t.Fatal(e)
		}
	}
	if g.State != "committed" || g.Active() {
		t.Fatal(g.State)
	}
	c, _ := Load(path)
	if c.Notice.Text != "candidate" {
		t.Fatal("healthy change was undone")
	}
	got, e := os.ReadFile(sentinel)
	if e != nil || !bytes.Equal(got, want) {
		t.Fatal("physical state was changed")
	}
	if e = RollbackGuard(path, g.ID); !errors.Is(e, ErrGuardConflict) {
		t.Fatal("completed guard was rolled back", e)
	}
}
func TestGuardRollbackRetainsNewIdentityFenceAndOriginalClient(t *testing.T) {
	path, u := guardFixture(t)
	now := time.Unix(3000, 0)
	g, _, e := BeginGuard(path, u, strings.Repeat("b", 32), []byte(`{"operation":"client_add","name":"new"}`), now)
	if e != nil {
		t.Fatal(e)
	}
	var created User
	if e = ApplyGuard(path, g.ID, now, func(c *Config) error { var e error; created, e = c.AddUser("new"); return e }); e != nil {
		t.Fatal(e)
	}
	if e = RollbackGuard(path, g.ID); e != nil {
		t.Fatal(e)
	}
	c, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Users) != 1 || c.Users[0] != u || len(c.RetiredUsers) != 1 || c.RetiredUsers[0].ID != created.ID {
		t.Fatal("rollback reused a published identity")
	}
}
func TestGuardArmedCrashAndAuthorizationAreFailClosed(t *testing.T) {
	path, u := guardFixture(t)
	now := time.Unix(4000, 0)
	bad := u
	bad.Key = strings.Repeat("0", 64)
	if _, _, e := BeginGuard(path, bad, strings.Repeat("c", 32), []byte(`{"operation":"service_stop"}`), now); e == nil {
		t.Fatal("revoked key armed watchdog")
	}
	g, _, e := BeginGuard(path, u, strings.Repeat("c", 32), []byte(`{"operation":"service_stop"}`), now)
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := GuardStatus(path)
	if e != nil || reopened.ID != g.ID || !reopened.Expired(now.Add(time.Minute)) {
		t.Fatal("armed trial did not survive reopening", e)
	}
	if e = RollbackGuard(path, g.ID); e != nil {
		t.Fatal(e)
	}
	c, e := Load(path)
	if e != nil || c.Notice.Text != "original" {
		t.Fatal("unapplied rollback damaged state", e)
	}
	raw, _ := json.Marshal(g)
	if !bytes.Contains(raw, []byte("before")) {
		t.Fatal("durable snapshot missing")
	}
}
