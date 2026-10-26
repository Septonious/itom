package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

type refusedStartupNetwork struct{}

func (refusedStartupNetwork) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("secret-provider-password-private.example")
}

func TestServerTraceExistsWhenStartupFailsBeforeAnyAccountConnects(t *testing.T) {
	root := t.TempDir()
	p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: wire.ID{1}, Provider: "fixture", User: servercfg.User{ID: "12345678", Name: "field", Enabled: true, Key: strings.Repeat("17", 32), Up: "itom-field-up", Down: "itom-field-down"}, Accounts: []servercfg.Account{{ID: "account1", Mail: mailbox.Config{Host: "private.example", Port: 993, TLS: "tls", Username: "private-login", Password: "private-password"}}}}
	trace := &serverTrace{root: root, enabled: true}
	err := serveProfile(context.Background(), p, root, nil, nil, refusedStartupNetwork{}, nil, trace)
	if err == nil {
		t.Fatal("startup unexpectedly succeeded")
	}
	raw, err := os.ReadFile(filepath.Join(root, "transport-trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "IMAP_CONNECT_FAILED") || !strings.Contains(string(raw), "inventory_failed") {
		t.Fatal("missing pre-connection failure stages", string(raw))
	}
	for _, secret := range []string{"private.example", "private-login", "private-password", p.User.Key} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private input leaked to trace")
		}
	}
	if _, err = os.Stat(filepath.Join(root, "v2-12345678.json")); err != nil {
		t.Fatal("no failed-start snapshot", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatal("invalid JSONL event")
		}
	}
}

func TestServerTraceRotatesConcurrentWritersWithinBoundedPrivateFiles(t *testing.T) {
	trace := &serverTrace{root: t.TempDir(), enabled: true}
	var workers sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		workers.Go(func() {
			for n := 0; n < 12; n++ {
				if err := trace.record(map[string]any{"Event": "bounded_test", "Detail": strings.Repeat("x", 100<<10)}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	workers.Wait()
	files, err := filepath.Glob(filepath.Join(trace.root, "transport-trace.jsonl*"))
	if err != nil || len(files) != 2 {
		t.Fatal("unexpected trace rotation", files, err)
	}
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Size() > 2<<20 || info.Mode().Perm() != 0600 {
			t.Fatal("trace bound or permissions", info, err)
		}
		raw, _ := os.ReadFile(path)
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if !json.Valid([]byte(line)) {
				t.Fatal("concurrent events interleaved")
			}
		}
	}
	off := &serverTrace{root: t.TempDir(), enabled: false}
	if err := off.record(map[string]string{"Event": "disabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(off.root, "transport-trace.jsonl")); !os.IsNotExist(err) {
		t.Fatal("disabled trace wrote a file", err)
	}
}
