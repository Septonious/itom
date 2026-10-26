package tunnel_test

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/manager"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/transport"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

func TestFailedStartupCanExportServerTraceBeforeAnyClientConnects(t *testing.T) {
	root := t.TempDir()
	if err := tunnel.ServeManaged(context.Background(), servercfg.Config{}, filepath.Join(root, "tunnel-server"), nil, nil); !errors.Is(err, servercfg.ErrConfig) {
		t.Fatal("invalid configuration unexpectedly started", err)
	}
	archive := filepath.Join(t.TempDir(), "diagnostics.zip")
	if err := manager.ExportDiagnostics(root, archive); err != nil {
		t.Fatal("failed startup diagnostics unavailable", err)
	}
	z, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	found := false
	for _, f := range z.File {
		if f.Name == "transport-trace.jsonl" {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := io.ReadAll(r)
			r.Close()
			if err != nil || !strings.Contains(string(raw), "configuration_failed") {
				t.Fatal("missing cause in exported startup trace", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("server bundle omitted pre-connection trace")
	}
}

func profile(t *testing.T, accounts int) servercfg.Profile {
	t.Helper()
	p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: wire.ID{1}, Provider: "fixture", User: servercfg.User{ID: "12345678", Name: "fixture", Key: strings.Repeat("17", 32), Up: "itom-fixture-up", Down: "itom-fixture-down", Enabled: true}, Transport: servercfg.Transport{Layout: "split", DataShards: 4, AccountSessions: 5, EndpointConnections: 128, Participants: 1}}
	for n := 1; n <= accounts; n++ {
		p.Accounts = append(p.Accounts, servercfg.Account{ID: fmt.Sprintf("account%d", n), Mail: mailbox.Config{Host: "imap.example", Port: 993, TLS: "tls", Username: fmt.Sprintf("fixture%d@example.test", n), Password: "fixture-only"}})
	}
	return p
}
func folders(p servercfg.Profile) []string {
	out := []string{p.User.Up, p.User.Down, p.User.Up + "-session-v2", p.User.Down + "-session-v2"}
	for _, base := range []string{p.User.Up, p.User.Down} {
		for n := range 4 {
			out = append(out, fmt.Sprintf("%s-data-%02d", base, n))
		}
	}
	return out
}
func store(t *testing.T, p servercfg.Profile, account, path string, fs []string) *journal.Store {
	t.Helper()
	limit := pool.Amount{Bytes: 64 << 20, Messages: 128}
	cfg := journal.Config{Accounts: []journal.Account{{ID: account, Provider: p.Provider, Limit: limit, Users: []journal.User{{ID: p.User.ID, Limit: limit, Folders: fs}}}}, MaxGrants: 128, MaxAttempts: 256}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := journal.Open(path, cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func bindings(t *testing.T, p servercfg.Profile) []tunnel.AccountJournals {
	t.Helper()
	var out []tunnel.AccountJournals
	for _, a := range p.Accounts {
		dir := filepath.Join(t.TempDir(), a.ID, p.User.ID)
		out = append(out, tunnel.AccountJournals{Account: a.ID, Control: store(t, p, a.ID, dir+"-control.db", []string{p.User.Up, p.User.Down, p.User.Up + "-session-v2", p.User.Down + "-session-v2"}), Data: store(t, p, a.ID, dir+"-data.db", folders(p))})
	}
	return out
}
func budgets(n int) (transport.RuntimeBudget, transport.RuntimeBudget) {
	return transport.RuntimeBudget{Connections: 2 * n, PerAccount: 2}, transport.RuntimeBudget{Connections: 3 * n, PerAccount: 3}
}

func TestProfilePlanUsesOriginalPerAccountIssuersAndAllTenRetainedFolders(t *testing.T) {
	p := profile(t, 10)
	b := bindings(t, p)
	client, server := budgets(10)
	plan, err := tunnel.PlanProfileService(p, b, client, server)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Accounts) != 10 || plan.ClientBudget != client || plan.ServerBudget != server {
		t.Fatal(plan)
	}
	if _, err := plan.SharedRosterPlan(); !errors.Is(err, tunnel.ErrPerAccountRoster) {
		t.Fatal("distinct installed issuers flattened into anchor roster", err)
	}
	seen := map[wire.ID]bool{}
	for _, a := range plan.Accounts {
		if a.FastFolder != p.User.Up+"-session-v2" || a.BulkFolder != p.User.Down+"-session-v2" || len(a.InventoryFolders) != journal.MaxFoldersPerUser {
			t.Fatal("legacy folder coverage lost", a)
		}
		if seen[a.ControlStore] || seen[a.DataStore] || a.ControlStore == a.DataStore {
			t.Fatal("per-account files were flattened", a)
		}
		seen[a.ControlStore] = true
		seen[a.DataStore] = true
	}
	for _, a := range b {
		if _, err := a.Data.Issue(journal.Scope{Account: a.Account, User: p.User.ID, Session: wire.ID{2}}, pool.Amount{Bytes: 1, Messages: 1}); !errors.Is(err, journal.ErrRecovery) {
			t.Fatal("planning enabled admission before actual inventory", err)
		}
		if err := a.Data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	if p.Transport.AccountSessions != 5 || len(p.Accounts) != 10 {
		t.Fatal("planning mutated profile")
	}
}

func TestProfilePlanRejectsMissingScopeSharedLoginOvercommitAndFolderOverflow(t *testing.T) {
	p := profile(t, 1)
	b := bindings(t, p)
	client, server := budgets(1)
	original := b[0].Data.Config()
	for name, change := range map[string]func(*servercfg.Profile, []tunnel.AccountJournals){
		"missing_folder":          func(p *servercfg.Profile, _ []tunnel.AccountJournals) { p.User.Up = "itom-absent-up" },
		"wrong_user":              func(p *servercfg.Profile, _ []tunnel.AccountJournals) { p.User.ID = "87654321" },
		"wrong_provider":          func(p *servercfg.Profile, _ []tunnel.AccountJournals) { p.Provider = "foreign" },
		"two_users_exceed_logins": func(p *servercfg.Profile, _ []tunnel.AccountJournals) { p.Transport.Participants = 2 },
		"endpoint_cap":            func(p *servercfg.Profile, _ []tunnel.AccountJournals) { p.Transport.EndpointConnections = 2 },
		"same_issuer":             func(_ *servercfg.Profile, b []tunnel.AccountJournals) { b[0].Data = b[0].Control },
	} {
		t.Run(name, func(t *testing.T) {
			q := p
			bs := append([]tunnel.AccountJournals(nil), b...)
			change(&q, bs)
			if _, err := tunnel.PlanProfileService(q, bs, client, server); err == nil {
				t.Fatal("unsafe profile accepted")
			}
		})
	}
	overflow := append([]tunnel.AccountJournals(nil), b...)
	overflow[0].Control = store(t, p, p.Accounts[0].ID, filepath.Join(t.TempDir(), "other-control.db"), []string{p.User.Up, p.User.Down, p.User.Up + "-session-v2", p.User.Down + "-session-v2", "itom-extra-retained"})
	if _, err := tunnel.PlanProfileService(p, overflow, client, server); !errors.Is(err, tunnel.ErrProfileCapacity) {
		t.Fatal("combined retained folder limit bypassed", err)
	}
	if !reflect.DeepEqual(original, b[0].Data.Config()) {
		t.Fatal("planner changed persistent folder/limit profile")
	}
	// Insufficient service capacity refuses the complete selection; it cannot
	// silently truncate the configured account list to the sockets it can fit.
	q := profile(t, 65)
	c, s := budgets(65)
	if _, err := tunnel.PlanProfileService(q, nil, c, s); !errors.Is(err, tunnel.ErrProfileCapacity) {
		t.Fatal("configured accounts silently truncated", err)
	}
}

func TestProfilePlanPreservesUnknownOldShardDebtAndInventoryFence(t *testing.T) {
	p := profile(t, 1)
	b := bindings(t, p)
	s := b[0].Data
	account := b[0].Account
	if err := s.EnableAfterInventory(account, p.User.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: account, User: p.User.ID, Session: wire.ID{3}}
	grant, err := s.Issue(scope, pool.Amount{Bytes: 10000, Messages: 10})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.PrepareStarted(grant.Ref, p.User.Up+"-data-03", wire.ID{4}, 4000, []byte("retained original body"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteAndClose(a.Ref, journal.Uncertain); err != nil {
		t.Fatal(err)
	}
	held, err := s.Held(account, p.User.ID)
	if err != nil {
		t.Fatal(err)
	}
	client, server := budgets(1)
	plan, err := tunnel.PlanProfileService(p, b, client, server)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := plan.SharedRosterPlan(); err != nil || len(r.Accounts) != 1 {
		t.Fatal("original single-account identity cannot use scoped roster", r, err)
	}
	got, err := s.GetAttempt(a.Ref)
	if err != nil || got.State != journal.Unknown {
		t.Fatal("planning forgot uncertain legacy attempt", got, err)
	}
	now, err := s.Held(account, p.User.ID)
	if err != nil || held != now {
		t.Fatal("planning released original debt", held, now, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}
