package manager

import (
	"encoding/base64"
	"errors"
	"itom.local/core/servercfg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clientUI(t *testing.T, input string) (*UI, servercfg.User, servercfg.User) {
	u, _ := accountUI(t, input)
	var first, second servercfg.User
	if err := servercfg.Update(u.db(), func(c *servercfg.Config) error {
		var err error
		first, err = c.AddUser("same name")
		if err != nil {
			return err
		}
		second, err = c.AddUser("same name")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u, first, second
}
func TestExportClientByNumberAndRemoveKeepsJournals(t *testing.T) {
	u, a, b := clientUI(t, "2\n")
	if err := u.export(); err != nil {
		t.Fatal(err)
	}
	out := u.Out.(interface{ String() string }).String()
	start := strings.Index(out, "itom://import#")
	if start < 0 {
		t.Fatal(out)
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(out[start+len("itom://import#"):]))
	if err != nil || !strings.Contains(string(body), b.ID) || strings.Contains(string(body), a.ID) {
		t.Fatal("wrong client exported", err)
	}
	u, a, b = clientUI(t, "4\n1\nyes\n")
	journal := filepath.Join(u.State, "tunnel-server", "a01", a.ID, "data.db")
	if err := os.MkdirAll(filepath.Dir(journal), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, []byte("unresolved debt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := u.users(); err != nil {
		t.Fatal(err)
	}
	c, err := servercfg.Load(u.db())
	if err != nil || len(c.Users) != 1 || c.Users[0].ID != b.ID {
		t.Fatal(c, err)
	}
	raw, err := os.ReadFile(journal)
	if err != nil || string(raw) != "unresolved debt" {
		t.Fatal("removal destroyed debt", err)
	}
}
func TestClientSelectionCancellationAndDisabledExports(t *testing.T) {
	u, _, _ := clientUI(t, "0\n")
	if err := u.export(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(u.Out.(interface{ String() string }).String(), "itom://") {
		t.Fatal("cancel exported secret")
	}
	u, a, b := clientUI(t, "1\n")
	if err := servercfg.Update(u.db(), func(c *servercfg.Config) error { c.Users[0].Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := u.export(); err != nil {
		t.Fatal(err)
	}
	out := u.Out.(interface{ String() string }).String()
	if strings.Contains(out, a.ID) || !strings.Contains(out, b.ID) {
		t.Fatal("disabled client offered for export", out)
	}
}
func TestUninstallStopsBeforeDeletingAndPreservesState(t *testing.T) {
	dir := t.TempDir()
	binary, service := filepath.Join(dir, "itom"), filepath.Join(dir, "itom.service")
	state := filepath.Join(dir, "server.db")
	for p, b := range map[string]string{binary: "binary", service: unit, state: "durable keys and debt"} {
		if err := os.WriteFile(p, []byte(b), 0600); err != nil {
			t.Fatal(err)
		}
	}
	failed := errors.New("stop failed")
	if err := uninstallFiles(binary, service, func(...string) error { return failed }); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("stop failure deleted binary")
	}
	var calls []string
	if err := uninstallFiles(binary, service, func(args ...string) error { calls = append(calls, strings.Join(args, " ")); return nil }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, "|") != "stop itom.service|disable itom.service|daemon-reload" {
		t.Fatal(calls)
	}
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Fatal("binary not removed", err)
	}
	if raw, err := os.ReadFile(state); err != nil || string(raw) != "durable keys and debt" {
		t.Fatal("state lost", err)
	}
}
func TestTrafficRateHandlesRepeatedSamplesAndRestart(t *testing.T) {
	if trafficRate(1_000_000, 0, 1000) != "8.00" || trafficRate(0, 1000, 1000) != "—" || trafficRate(1000, 0, 0) != "—" {
		t.Fatal("invalid rate conversion/reset")
	}
}
