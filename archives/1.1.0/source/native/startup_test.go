package mobile

import (
	"encoding/json"
	"strings"
	"testing"
)

type startupHost struct{ lookups int }

func (*startupHost) Protect(int) bool       { return false }
func (*startupHost) DNS() string            { return `["192.0.2.53"]` }
func (h *startupHost) Lookup(string) string { h.lookups++; return `[]` }
func (*startupHost) Route(string) string    { return "PROXY" }

// This is the production VaultStore.runtime JSON shape, including Android's
// profile discriminator. The public API fixture previously constructed a CLI
// profile directly and therefore missed this boundary entirely.
func TestAndroidRuntimeProfileReachesProtectedNetworkStartup(t *testing.T) {
	raw := `{"Profile":{"schema":1,"kind":"itom-android","server":[1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"provider":"rambler","user":{"uid":"12345678","name":"field","enabled":true,"key":"` + strings.Repeat("17", 32) + `","up":"itom-field-up","down":"itom-field-down"},"accounts":[{"id":"account1","imap":{"host":"fixture.invalid","dial_ip":"","port":993,"tls":"tls","username":"fixture","password":"fixture-only"}}],"transport":{"layout":"auto","data_shards":1,"account_sessions":10,"endpoint_connections":128,"login_parallelism":4,"participants":1}},"DNS":["192.0.2.53"],"Socks":{"Port":1080,"Authentication":false},"Diagnostics":true}`
	for _, kind := range []string{"itom-android", "itom-imap-probe", "unrecognized-profile"} {
		t.Run(kind, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				t.Fatal(err)
			}
			value["Profile"].(map[string]any)["kind"] = kind
			encoded, _ := json.Marshal(value)
			host := &startupHost{}
			client, err := New(string(encoded), t.TempDir(), host)
			if client != nil {
				client.Close()
				t.Fatal("empty platform lookup must not start a client")
			}
			if err == nil {
				t.Fatal("empty platform lookup must fail")
			}
			if kind == "unrecognized-profile" {
				if host.lookups != 0 {
					t.Fatal("unknown profile reached the network")
				}
				if err.Error() != "V2_STARTUP_PROFILE" {
					t.Fatalf("missing safe profile failure phase: %v", err)
				}
			} else if host.lookups == 0 {
				t.Fatalf("valid Android runtime was rejected before protected network startup: %v", err)
			} else if err.Error() != "V2_STARTUP_IMAP" {
				t.Fatalf("missing safe network startup phase: %v", err)
			}
		})
	}
}
