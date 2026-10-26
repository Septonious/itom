package mobile

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type startupHost struct{ lookups int }

func (*startupHost) Protect(int) bool       { return false }
func (*startupHost) DNS() string            { return `["192.0.2.53"]` }
func (h *startupHost) Lookup(string) string { h.lookups++; return `[]` }
func (*startupHost) Route(string) string    { return "PROXY" }
func (*startupHost) Startup(string) bool    { return true }

// This is the production VaultStore.runtime JSON shape, including Android's
// profile discriminator. The public API fixture previously constructed a CLI
// profile directly and therefore missed this boundary entirely.
func androidRuntimeFixture() string {
	return `{"Profile":{"schema":1,"kind":"itom-android","server":[1,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"provider":"rambler","user":{"uid":"12345678","name":"field","enabled":true,"key":"` + strings.Repeat("17", 32) + `","up":"itom-field-up","down":"itom-field-down"},"accounts":[{"id":"account1","imap":{"host":"fixture.invalid","dial_ip":"","port":993,"tls":"tls","username":"fixture","password":"fixture-only"}}],"transport":{"layout":"auto","data_shards":1,"account_sessions":10,"endpoint_connections":128,"login_parallelism":4,"participants":1}},"DNS":["192.0.2.53"],"Socks":{"Port":1080,"Authentication":false},"Diagnostics":true}`
}

func TestAndroidRuntimeProfileReachesProtectedNetworkStartup(t *testing.T) {
	raw := androidRuntimeFixture()
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

type cancelStartupHost struct {
	startupHost
	entered, release chan struct{}
	once             sync.Once
	reports          atomic.Int32
}

func (h *cancelStartupHost) Lookup(string) string {
	close(h.entered)
	<-h.release
	return `[]`
}
func (h *cancelStartupHost) Startup(raw string) bool {
	if h.reports.Add(1) > 1 {
		h.once.Do(func() { close(h.release) })
		return false
	}
	return true
}

func TestUserCanCancelWhileNativeStartupWaitsForPlatformDNS(t *testing.T) {
	host := &cancelStartupHost{entered: make(chan struct{}), release: make(chan struct{})}
	defer host.once.Do(func() { close(host.release) })
	directory := t.TempDir()
	done := make(chan error, 1)
	go func() {
		c, err := New(androidRuntimeFixture(), directory, host)
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	select {
	case <-host.entered:
	case <-timer.C:
		t.Fatal("startup never reached platform DNS")
	}
	select {
	case err := <-done:
		if err == nil || host.reports.Load() < 2 {
			t.Fatal("cancel did not interrupt native startup", err)
		}
	case <-timer.C:
		t.Fatal("user cancellation could not interrupt connecting")
	}
}
