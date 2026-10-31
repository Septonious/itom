package tunnel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

func TestClientStartupTraceSurvivesImmediateFailureWithoutAClientObject(t *testing.T) {
	for _, tracing := range []bool{false, true} {
		t.Run(map[bool]string{false: "minimal", true: "tracing"}[tracing], func(t *testing.T) {
			root := t.TempDir()
			p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: wire.ID{1}, Provider: "fixture", User: servercfg.User{ID: "12345678", Name: "field", Enabled: true, Key: strings.Repeat("17", 32), Up: "itom-field-up", Down: "itom-field-down"}, Accounts: []servercfg.Account{{ID: "account1", Mail: mailbox.Config{Host: "private.example", Port: 993, TLS: "tls", Username: "private-login", Password: "private-password"}}}}
			client, err := StartClientWithOptions(context.Background(), p, root, refusedStartupNetwork{}, ClientOptions{Diagnostics: tracing})
			if client != nil || err == nil || err.Error() != "V2_STARTUP_IMAP" {
				t.Fatal("expected construction failure before a runtime client exists", err)
			}
			raw, err := os.ReadFile(filepath.Join(root, "startup-trace.jsonl"))
			if err != nil {
				t.Fatal("immediate failure left no exportable startup trace", err)
			}
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			var first, last struct{ Stage, FailureReason string }
			if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
				t.Fatal(err)
			}
			if first.Stage != "PROFILE" || last.Stage != "IMAP" || last.FailureReason != "IMAP_CONNECT_FAILED" {
				t.Fatal("trace lost attempt beginning or terminal cause", string(raw))
			}
			accountRaw, accountErr := os.ReadFile(filepath.Join(root, "transport-trace.jsonl"))
			if tracing && (accountErr != nil || !strings.Contains(string(accountRaw), "imap_CONNECT")) {
				t.Fatal("enabled account tracing missed failed connection", accountErr, string(accountRaw))
			}
			if !tracing && !os.IsNotExist(accountErr) {
				t.Fatal("packet tracing disabled but account trace was created", accountErr)
			}
			for _, secret := range []string{"private.example", "private-login", "private-password", p.User.Key} {
				if strings.Contains(string(raw)+string(accountRaw), secret) {
					t.Fatal("startup trace exposed private input")
				}
			}
		})
	}
}
