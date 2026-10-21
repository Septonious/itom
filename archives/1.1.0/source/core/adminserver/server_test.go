package adminserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"itom.local/core/adminapi"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func adminFixture(t *testing.T) (*Server, servercfg.User, servercfg.User) {
	t.Helper()
	state := t.TempDir()
	c, e := servercfg.New("admin-tests")
	if e != nil {
		t.Fatal(e)
	}
	u, e := c.AddUser("admin")
	if e != nil {
		t.Fatal(e)
	}
	other, e := c.AddUser("ordinary")
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.AddAccount(mailbox.Config{Host: "mail.example.invalid", Port: 993, TLS: "tls", Username: "account-marker", Password: "never-in-status"})
	if e != nil {
		t.Fatal(e)
	}
	if e = servercfg.Update(filepath.Join(state, "server.db"), func(dst *servercfg.Config) error { *dst = c; return nil }); e != nil {
		t.Fatal(e)
	}
	s := New(context.Background(), state)
	t.Cleanup(s.Close)
	return s, u, other
}
func adminCall(t *testing.T, s *Server, u servercfg.User, key string, q adminapi.Request) adminapi.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, handled, e := s.Dial(ctx, u, wire.Connect{Host: adminapi.Host, Port: adminapi.Port})
	if e != nil || !handled {
		t.Fatal(handled, e)
	}
	raw, _ := json.Marshal(q)
	r, e := adminapi.Call(ctx, conn, raw, key)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestAdminRoleAndPrivateKeyFenceVPNApplications(t *testing.T) {
	s, u, ordinary := adminFixture(t)
	spoof := ordinary
	spoof.Name = "admin"
	if _, handled, e := s.Dial(context.Background(), spoof, wire.Connect{Host: adminapi.Host, Port: adminapi.Port}); !handled || e == nil {
		t.Fatal("profile display name granted admin access")
	}
	if r := s.Execute(context.Background(), spoof, adminapi.Request{Operation: "status"}); r.OK || r.Error != "ADMIN_ACCESS_DENIED" {
		t.Fatal(r)
	}
	// Same authenticated admin VPN session, but an ordinary routed application
	// does not possess the app's private key and cannot authorize a management RPC.
	r := adminCall(t, s, u, strings.Repeat("0", 64), adminapi.Request{Operation: "status"})
	if r.OK || r.Error != "ADMIN_ACCESS_DENIED" {
		t.Fatal("VPN access alone granted administration", r)
	}
	r = adminCall(t, s, u, u.Key, adminapi.Request{Operation: "status"})
	if !r.OK || r.Status == nil || !r.Status.Admin || r.Status.CurrentUID != u.ID {
		t.Fatal(r)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), u.Key) || strings.Contains(string(raw), "never-in-status") {
		t.Fatal("status disclosed credentials")
	}
	if c, handled, e := s.Dial(context.Background(), u, wire.Connect{Host: "public.example", Port: 443}); c != nil || handled || e != nil {
		t.Fatal("management changed ordinary target routing")
	}
}
func TestAdminRechecksRoleAfterSocketWasOpened(t *testing.T) {
	s, u, _ := adminFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, e := s.Dial(ctx, u, wire.Connect{Host: adminapi.Host, Port: adminapi.Port})
	if e != nil {
		t.Fatal(e)
	}
	if e = servercfg.Update(s.db(), func(c *servercfg.Config) error { c.Users[0].Name = "operator"; return nil }); e != nil {
		t.Fatal(e)
	}
	raw := []byte(`{"operation":"status"}`)
	r, e := adminapi.Call(ctx, conn, raw, u.Key)
	if e != nil || r.OK || r.Error != "ADMIN_ACCESS_DENIED" {
		t.Fatal(r, e)
	}
}
func TestAdminWatchdogArmFailureMakesNoConfigurationChange(t *testing.T) {
	s, u, _ := adminFixture(t)
	s.Launch = func(string) error { return errors.New("systemd refused") }
	r := s.Execute(context.Background(), u, adminapi.Request{Operation: "global_message", ID: strings.Repeat("d", 32), Text: "must not apply"})
	if r.OK || r.Error != "ADMIN_WATCHDOG_UNAVAILABLE" {
		t.Fatal(r)
	}
	c, e := servercfg.Load(s.db())
	if e != nil || c.Notice.Text != "" {
		t.Fatal("mutation preceded watchdog", e)
	}
	g, e := servercfg.GuardStatus(s.db())
	if e != nil || g.Active() {
		t.Fatal("failed arm retained config lock", e)
	}
}
func TestAdminIrreversibleAndUnknownOperationsRemainCLIOnly(t *testing.T) {
	s, u, _ := adminFixture(t)
	s.Launch = func(string) error { t.Fatal("irreversible operation armed watchdog"); return nil }
	for _, op := range []string{"purge", "purge_all", "uninstall", "journal-profile", "shell", "exec", "init", "download_binary"} {
		if r := s.Execute(context.Background(), u, adminapi.Request{Operation: op, ID: strings.Repeat("e", 32)}); r.OK || r.Error != "ADMIN_OPERATION_UNAVAILABLE" {
			t.Fatal(op, r)
		}
	}
}
func TestWatchdogReopensTrialAndRestoresAfterSixtySeconds(t *testing.T) {
	s, u, _ := adminFixture(t)
	now := time.Unix(10000, 0)
	s.Now = func() time.Time { return now }
	s.Launch = func(string) error { return nil }
	q := adminapi.Request{Operation: "global_message", ID: strings.Repeat("f", 32), Text: "candidate"}
	reply := s.Execute(context.Background(), u, q)
	if !reply.OK {
		t.Fatal(reply)
	}
	var calls []string
	var stopAt time.Time
	r := watchdogRuntime{now: func() time.Time { return now }, wait: func() { now = now.Add(time.Second) }, service: func(action string) error {
		calls = append(calls, action)
		if action == "stop" {
			stopAt = now
		}
		return nil
	}, install: func() error { t.Fatal("unexpected installation"); return nil }}
	if e := runGuard(s.State, q.ID, r); e != nil {
		t.Fatal(e)
	}
	// Arming delay and application occur at t+1s; no heartbeat means rollback at
	// exactly application+60s. The runner opens/closes the database on every turn.
	if stopAt.Sub(time.Unix(10000, 0)) != 61*time.Second {
		t.Fatalf("rollback start %v; calls %v", stopAt, calls)
	}
	c, e := servercfg.Load(s.db())
	if e != nil || c.Notice.Text != "" {
		t.Fatal("watchdog did not restore original", e)
	}
	g, _ := servercfg.GuardStatus(s.db())
	if g.State != "rolled_back" {
		t.Fatal(g.State)
	}
}
func TestWatchdogHealthyChallengeRoundTripsCommit(t *testing.T) {
	s, u, _ := adminFixture(t)
	now := time.Unix(20000, 0)
	s.Now = func() time.Time { return now }
	s.Launch = func(string) error { return nil }
	q := adminapi.Request{Operation: "global_message", ID: strings.Repeat("1", 32), Text: "kept"}
	if r := s.Execute(context.Background(), u, q); !r.OK {
		t.Fatal(r)
	}
	r := watchdogRuntime{now: func() time.Time { return now }, wait: func() {
		now = now.Add(time.Second)
		g, _ := servercfg.GuardStatus(s.db())
		if g.State == "observing" && g.ActionDoneMS > 0 {
			if _, e := servercfg.BeatGuard(s.db(), u.ID, g.ID, g.Challenge, now); e != nil {
				t.Fatal(e)
			}
		}
	}, service: func(string) error { t.Fatal("notice change restarted service"); return nil }}
	if e := runGuard(s.State, q.ID, r); e != nil {
		t.Fatal(e)
	}
	g, _ := servercfg.GuardStatus(s.db())
	c, _ := servercfg.Load(s.db())
	if g.State != "committed" || c.Notice.Text != "kept" {
		t.Fatal(g.State, c.Notice.Text)
	}
}

func TestServiceInstallRollbackRestoresExactUnitAndBootLink(t *testing.T) {
	for _, kind := range []string{"enabled-regular", "disabled-regular", "enabled-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "itom.service")
			wants := filepath.Join(dir, "multi-user.target.wants")
			if e := os.MkdirAll(wants, 0755); e != nil {
				t.Fatal(e)
			}
			original := []byte("original ExecStart and service policy")
			if kind == "enabled-symlink" {
				if e := os.WriteFile(filepath.Join(dir, "vendor.service"), original, 0644); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink("vendor.service", path); e != nil {
					t.Fatal(e)
				}
			} else if e := os.WriteFile(path, original, 0644); e != nil {
				t.Fatal(e)
			}
			if strings.HasPrefix(kind, "enabled") {
				if e := os.Symlink("../itom.service", filepath.Join(wants, "itom.service")); e != nil {
					t.Fatal(e)
				}
			}
			snapshot, e := snapshotUnitFiles(dir)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.Remove(path); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(path, []byte("candidate"), 0644); e != nil {
				t.Fatal(e)
			}
			_ = os.Remove(filepath.Join(wants, "itom.service"))
			if e = os.Symlink("candidate-target", filepath.Join(wants, "itom.service")); e != nil {
				t.Fatal(e)
			}
			if e = restoreUnitFiles(dir, snapshot); e != nil {
				t.Fatal(e)
			}
			restored, e := snapshotUnitFiles(dir)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(restored, snapshot) {
				t.Fatalf("unit/boot state changed after rollback: %#v vs %#v", restored, snapshot)
			}
		})
	}
}
func TestWatchdogReopensAppliedConfigurationAfterCrash(t *testing.T) {
	s, u, _ := adminFixture(t)
	now := time.Unix(30000, 0)
	s.Now = func() time.Time { return now }
	s.Launch = func(string) error { return nil }
	q := adminapi.Request{Operation: "global_message", ID: strings.Repeat("2", 32), Text: "once"}
	if r := s.Execute(context.Background(), u, q); !r.OK {
		t.Fatal(r)
	}
	waits := 0
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("crash was not injected")
			}
		}()
		r := watchdogRuntime{now: func() time.Time { return now }, wait: func() {
			waits++
			now = now.Add(time.Second)
			if waits == 2 {
				panic("watchdog process died")
			}
		}, service: func(string) error { t.Fatal("notice must not restart service"); return nil }}
		_ = runGuard(s.State, q.ID, r)
	}()
	before, _ := servercfg.Load(s.db())
	g, _ := servercfg.GuardStatus(s.db())
	if g.State != "observing" {
		t.Fatal(g.State)
	}
	r := watchdogRuntime{now: func() time.Time { return now }, wait: func() {
		now = now.Add(time.Second)
		g, _ := servercfg.GuardStatus(s.db())
		if g.State == "observing" {
			_, e := servercfg.BeatGuard(s.db(), u.ID, g.ID, g.Challenge, now)
			if e != nil {
				t.Fatal(e)
			}
		}
	}, service: func(string) error { t.Fatal("recovery must not duplicate a completed service action"); return nil }}
	if e := runGuard(s.State, q.ID, r); e != nil {
		t.Fatal(e)
	}
	after, _ := servercfg.Load(s.db())
	g, _ = servercfg.GuardStatus(s.db())
	if g.State != "committed" || after.Notice.Revision != before.Notice.Revision {
		t.Fatal("resumed helper applied the change twice")
	}
}

func TestAdminCannotReplayProofOnAnotherTCPStream(t *testing.T) {
	s, u, _ := adminFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, _, e := s.Dial(ctx, u, wire.Connect{Host: adminapi.Host, Port: adminapi.Port})
	if e != nil {
		t.Fatal(e)
	}
	banner, e := bufio.NewReader(first).ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	first.Close()
	nonce := strings.TrimSuffix(strings.TrimPrefix(banner, "ITOM-ADMIN-1 "), "\n")
	raw := []byte(`{"operation":"status"}`)
	proof, e := adminapi.Proof(u.Key, nonce, raw)
	if e != nil {
		t.Fatal(e)
	}
	second, _, e := s.Dial(ctx, u, wire.Connect{Host: adminapi.Host, Port: adminapi.Port})
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	reader := bufio.NewReader(second)
	next, e := reader.ReadString('\n')
	if e != nil {
		t.Fatal(e)
	}
	if next == banner {
		t.Fatal("challenge reused")
	}
	req, _ := http.NewRequest(http.MethodPost, "http://"+adminapi.Host+"/rpc", bytes.NewReader(raw))
	req.Close = true
	req.Header.Set("X-ITOM-Admin-Proof", proof)
	if e = req.Write(second); e != nil {
		t.Fatal(e)
	}
	res, e := http.ReadResponse(reader, req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var response adminapi.Response
	if e = json.NewDecoder(res.Body).Decode(&response); e != nil {
		t.Fatal(e)
	}
	if response.OK || response.Error != "ADMIN_ACCESS_DENIED" {
		t.Fatal("proof replay granted access", response)
	}
}

func TestAdminRejectsStaleFormWithoutArmingOrOverwritingCLIChange(t *testing.T) {
	s, u, _ := adminFixture(t)
	c, _ := servercfg.Load(s.db())
	revision := configRevision(c)
	if e := servercfg.Update(s.db(), func(c *servercfg.Config) error { return c.SetNotice("CLI update") }); e != nil {
		t.Fatal(e)
	}
	s.Launch = func(string) error { t.Fatal("stale form armed a watchdog"); return nil }
	r := s.Execute(context.Background(), u, adminapi.Request{Operation: "global_message", ID: strings.Repeat("3", 32), Revision: revision, Text: "stale"})
	if r.OK || r.Error != "ADMIN_CHANGE_CONFLICT" {
		t.Fatal(r)
	}
	c, _ = servercfg.Load(s.db())
	if c.Notice.Text != "CLI update" {
		t.Fatal("CLI state overwritten")
	}
}
