// Verify the actual server executable with the Android native Go entry point.
// TLS IMAP and authenticated admin traffic are real; platform callbacks are
// local fixture callbacks. This is not an Android device or provider test.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"itom.local/core/adminapi"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	mobile "itom.local/mobile"
)

type descriptor struct {
	ClientPort int `json:"client_port"`
	ServerPort int `json:"server_port"`
	Root       string
	Accounts   int
}
type host struct {
	ctx       context.Context
	protected atomic.Uint64
	mu        sync.Mutex
	stages    []json.RawMessage
}

func (h *host) Protect(int) bool   { h.protected.Add(1); return true }
func (*host) DNS() string          { return `["127.0.0.1"]` }
func (*host) Lookup(string) string { return `["127.0.0.1"]` }
func (*host) Route(string) string  { return "VPN" }
func (h *host) Startup(raw string) bool {
	h.mu.Lock()
	if len(h.stages) < 100 {
		h.stages = append(h.stages, json.RawMessage(raw))
	}
	h.mu.Unlock()
	return h.ctx.Err() == nil
}

type round struct {
	Index                   int   `json:"index"`
	ServerRestart           bool  `json:"server_restart"`
	StartupMS               int64 `json:"startup_ms"`
	NativeRPCMS             int64
	ActiveLines, TotalLines int
	RTTMillis               int64
	ProtectedSockets        uint64            `json:"protected_sockets"`
	Stages                  []json.RawMessage `json:"startup_stages"`
}
type report struct {
	Success                   bool   `json:"success"`
	Error                     string `json:"error,omitempty"`
	Revision, ServerVersion   string
	Rounds                    []round
	ProviderAccounts          int
	AndroidInvitationExported bool
	ExportedProfileLoaded     bool
	PrivateStatePreserved     bool
	AndroidDeviceQualified    bool `json:"android_device_qualified"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (err error) {
	config := flag.String("provider", "", "generated provider descriptor")
	binary := flag.String("server", "", "actual server executable")
	state := flag.String("state", "", "private fixture state")
	out := flag.String("out", "", "result JSON")
	count := flag.Int("rounds", 5, "native startup cycles")
	flag.Parse()
	if *count < 1 || *count > 20 || *config == "" || *binary == "" || *state == "" || *out == "" {
		return errors.New("provider, server, state, out and 1..20 rounds are required")
	}
	r := report{Revision: mobile.Version()}
	defer func() {
		if err != nil {
			r.Error = err.Error()
		}
		raw, _ := json.MarshalIndent(r, "", "  ")
		_ = os.WriteFile(*out, raw, 0600)
	}()
	raw, err := os.ReadFile(*config)
	if err != nil {
		return err
	}
	var p descriptor
	if err = json.Unmarshal(raw, &p); err != nil {
		return err
	}
	r.ProviderAccounts = p.Accounts
	if err = os.Setenv("SSL_CERT_FILE", p.Root); err != nil {
		return err
	}
	version, err := exec.Command(*binary, "version").CombinedOutput()
	if err != nil {
		return errors.New("server version failed")
	}
	r.ServerVersion = strings.TrimSpace(string(version))
	if !strings.Contains(r.ServerVersion, tunnel.TransportRevision) {
		return errors.New("client/server revision mismatch")
	}
	cfg, err := servercfg.New("rambler")
	if err != nil {
		return err
	}
	user, err := cfg.AddUser("admin")
	if err != nil {
		return err
	}
	if _, err = cfg.AddUser("standby"); err != nil {
		return err
	}
	for i := 0; i < p.Accounts; i++ {
		name := fmt.Sprintf("a%02d", i+1)
		cfg.Accounts = append(cfg.Accounts, servercfg.Account{ID: name, Mail: mailbox.Config{Host: "127.0.0.1", Port: p.ServerPort, TLS: "tls", Username: name, Password: "fixture-only"}})
	}
	// Defaults deliberately match an imported existing profile, including two users.
	cfg.DiagnosticsMode = "on"
	serverState := filepath.Join(*state, "server")
	if err = os.MkdirAll(serverState, 0700); err != nil {
		return err
	}
	if err = servercfg.Update(filepath.Join(serverState, "server.db"), func(c *servercfg.Config) error { *c = cfg; return nil }); err != nil {
		return err
	}
	profilePath := filepath.Join(*state, "exported-profile.json")
	if err = cfg.Export(user.ID, profilePath); err != nil {
		return err
	}
	profile, err := servercfg.ReadProfile(profilePath)
	if err != nil {
		return err
	}
	r.ExportedProfileLoaded = true
	if err = cfg.ExportAndroid(user.ID, filepath.Join(*state, "android-invitation.json")); err != nil {
		return err
	}
	r.AndroidInvitationExported = true
	for i := range profile.Accounts {
		profile.Accounts[i].Mail.Port = p.ClientPort
	}
	profile.Kind = "itom-android"
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	var process *exec.Cmd
	var ended chan error
	var log *os.File
	stop := func() error {
		if process == nil {
			return nil
		}
		_ = process.Process.Signal(syscall.SIGTERM)
		select {
		case e := <-ended:
			process = nil
			log.Close()
			return e
		case <-time.After(5 * time.Second):
			_ = process.Process.Kill()
			<-ended
			process = nil
			log.Close()
			return errors.New("server failed to join")
		}
	}
	start := func() error {
		var e error
		log, e = os.OpenFile(filepath.Join(filepath.Dir(*out), "startup-server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		process = exec.CommandContext(ctx, *binary, "serve", "--state", serverState)
		process.Stdout, process.Stderr = log, log
		if e = process.Start(); e != nil {
			log.Close()
			process = nil
			return e
		}
		ended = make(chan error, 1)
		go func(c *exec.Cmd) { ended <- c.Wait() }(process)
		return nil
	}
	if err = start(); err != nil {
		return err
	}
	defer stop()
	for i := 0; i < *count; i++ {
		restarted := i > 0 && i%2 == 0
		if restarted {
			if err = stop(); err != nil {
				return err
			}
			if err = start(); err != nil {
				return err
			}
		}
		listen, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			return e
		}
		port := listen.Addr().(*net.TCPAddr).Port
		listen.Close()
		input, _ := json.Marshal(map[string]any{"Profile": profile, "DNS": []string{"127.0.0.1"}, "Socks": map[string]any{"Port": port, "Authentication": false}, "Diagnostics": true})
		startup, stopStartup := context.WithTimeout(ctx, 45*time.Second)
		h := &host{ctx: startup}
		begin := time.Now()
		client, e := mobile.New(string(input), filepath.Join(*state, "phone"), h)
		stopStartup()
		if e != nil {
			return fmt.Errorf("native startup %d: %w", i, e)
		}
		row := round{Index: i, ServerRestart: restarted, StartupMS: time.Since(begin).Milliseconds()}
		e = func() error {
			defer client.Close()
			deadline := time.Now().Add(30 * time.Second)
			for {
				var snapshot struct {
					ActiveLines, TotalLines int
					RTTMillis               int64
					Error                   string
				}
				if e := json.Unmarshal([]byte(client.Snapshot()), &snapshot); e != nil {
					return e
				}
				if snapshot.Error != "" {
					return fmt.Errorf("native failure: %s", snapshot.Error)
				}
				if snapshot.TotalLines == 3*p.Accounts && snapshot.ActiveLines == snapshot.TotalLines && snapshot.RTTMillis > 0 {
					row.ActiveLines, row.TotalLines, row.RTTMillis = snapshot.ActiveLines, snapshot.TotalLines, snapshot.RTTMillis
					break
				}
				if time.Now().After(deadline) {
					return errors.New("all native roles or measured ping missing")
				}
				time.Sleep(25 * time.Millisecond)
			}
			begin := time.Now()
			answer, e := client.AdminCall(`{"operation":"status"}`)
			if e != nil {
				return e
			}
			row.NativeRPCMS = time.Since(begin).Milliseconds()
			var reply adminapi.Response
			if e = json.Unmarshal([]byte(answer), &reply); e != nil {
				return e
			}
			if !reply.OK || reply.Status == nil || !reply.Status.Admin || reply.Status.CurrentUID != user.ID || len(reply.Status.Accounts) != p.Accounts {
				return errors.New("native authenticated admin status invalid")
			}
			return nil
		}()
		h.mu.Lock()
		row.Stages = append(row.Stages, h.stages...)
		h.mu.Unlock()
		row.ProtectedSockets = h.protected.Load()
		r.Rounds = append(r.Rounds, row)
		if e != nil {
			return fmt.Errorf("round %d: %w", i, e)
		}
		if row.ProtectedSockets == 0 {
			return errors.New("native protection callback not used")
		}
	}
	if err = stop(); err != nil {
		return err
	}
	r.PrivateStatePreserved = true
	r.Success = true
	return nil
}
