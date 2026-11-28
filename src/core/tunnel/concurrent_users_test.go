//go:build linux

package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// Uses the same verified-TLS fixture as the native-process integration test.
// Independent users share every physical mailbox and obey the configured socket budget.
func testConcurrentUsers(t *testing.T, cfg servercfg.Config, origin string, mailPEM, httpsPEM []byte, peak *atomic.Int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cfg.Transport.AccountSessions = 16
	root := t.TempDir()
	ports := map[string]uint16{cfg.Users[0].ID: 10001, cfg.Users[1].ID: 10002}
	done := make(chan error, 1)
	go func() {
		done <- ServeManaged(ctx, cfg, filepath.Join(root, "server"), nil, func(ctx context.Context, user servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
			if target.Host != "203.0.113.80" || target.Port != ports[user.ID] {
				return nil, true, errors.New("FIXTURE_USER_POLICY")
			}
			c, err := (&net.Dialer{}).DialContext(ctx, "tcp", origin)
			if err != nil {
				return nil, true, err
			}
			return c.(*net.TCPConn), true, nil
		})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("multi-user server did not stop")
		}
	}()
	mailRoots, httpsRoots := x509.NewCertPool(), x509.NewCertPool()
	if !mailRoots.AppendCertsFromPEM(mailPEM) || !httpsRoots.AppendCertsFromPEM(httpsPEM) {
		t.Fatal("fixture roots")
	}
	type joined struct {
		i   int
		c   *Client
		err error
	}
	joins := make(chan joined, 2)
	for i, user := range cfg.Users {
		go func() {
			p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: cfg.Server, Provider: cfg.Provider, Accounts: cfg.Accounts, User: user, Transport: cfg.TransportForUsers()}
			c, err := StartClientWithOptions(ctx, p, filepath.Join(root, fmt.Sprint(i)), &net.Dialer{}, ClientOptions{TLSConfig: &tls.Config{RootCAs: mailRoots}})
			joins <- joined{i, c, err}
		}()
	}
	clients := make([]*Client, 2)
	defer func() {
		for _, c := range clients {
			if c != nil {
				c.Close()
			}
		}
	}()
	for range 2 {
		j := <-joins
		if j.err != nil {
			t.Fatal("concurrent authenticated startup", j.err)
		}
		clients[j.i] = j.c
		if s := j.c.Snapshot(); s.ActiveLines != 4*len(cfg.Accounts) || s.TotalLines != 4*len(cfg.Accounts) {
			t.Fatalf("four real lines were not ready: %+v", s.Stats)
		}
	}
	transfer := func(i int) error {
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: httpsRoots}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			stream, _, err := clients[i].Open(ctx, wire.Connect{Host: "203.0.113.80", Port: ports[cfg.Users[i].ID]})
			if err != nil {
				return nil, err
			}
			conn, ok := stream.(net.Conn)
			if !ok {
				stream.Close()
				return nil, errors.New("fixture stream is not net.Conn")
			}
			return conn, nil
		}}
		defer tr.CloseIdleConnections()
		payload := bytes.Repeat([]byte{byte(0x41 + i)}, 256<<10)
		res, err := (&http.Client{Transport: tr, Timeout: 15 * time.Second}).Post("https://example.com/", "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 || !bytes.Equal(raw, payload) {
			return fmt.Errorf("user %d HTTPS integrity/status: %v", i, err)
		}
		return nil
	}
	errs := make(chan error, 2)
	for i := range clients {
		go func() { errs <- transfer(i) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	// The authenticated identity must reach its own exit policy only.
	if c, _, err := clients[0].Open(ctx, wire.Connect{Host: "203.0.113.80", Port: ports[cfg.Users[1].ID]}); err == nil {
		c.Close()
		t.Fatal("first user reached second user's exit policy")
	}
	clients[0].Close()
	clients[0] = nil
	if err := transfer(1); err != nil {
		t.Fatal("one user's disconnect interrupted the other", err)
	}
	if peak.Load() > int64(16*len(cfg.Accounts)) {
		t.Fatal("provider connection budget exceeded", peak.Load())
	}
	t.Logf("two simultaneous users: all configured lines, HTTPS integrity, exit isolation, independent disconnect; peak %d", peak.Load())
}

// The >10 account case is deliberately sequential: the first admission owns
// the first eight accounts and the second owns the remaining seven. Test the
// actual authenticated grant transition, not just an in-memory planner.
func testSequentialUsers(t *testing.T, cfg servercfg.Config, origin string, mailPEM, httpsPEM []byte, peak *atomic.Int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	cfg.Transport.AccountSessions = 16
	cfg.Transport.EndpointConnections = 128
	root := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- ServeManaged(ctx, cfg, filepath.Join(root, "server"), nil, func(ctx context.Context, user servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
			if target.Host != "203.0.113.80" || target.Port != 443 {
				return nil, true, errors.New("FIXTURE_USER_POLICY")
			}
			c, e := (&net.Dialer{}).DialContext(ctx, "tcp", origin)
			if e != nil {
				return nil, true, e
			}
			return c.(*net.TCPConn), true, nil
		})
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("sequential server did not stop")
		}
	}()
	mailRoots, httpsRoots := x509.NewCertPool(), x509.NewCertPool()
	if !mailRoots.AppendCertsFromPEM(mailPEM) || !httpsRoots.AppendCertsFromPEM(httpsPEM) {
		t.Fatal("fixture roots")
	}
	connect := func(i int) *Client {
		p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: cfg.Server, Provider: cfg.Provider, User: cfg.Users[i], Accounts: cfg.Accounts, Transport: cfg.TransportForUsers()}
		c, e := StartClientWithOptions(ctx, p, filepath.Join(root, fmt.Sprint(i)), &net.Dialer{}, ClientOptions{TLSConfig: &tls.Config{RootCAs: mailRoots}})
		if e != nil {
			t.Fatalf("user %d startup: %v", i, e)
		}
		return c
	}
	waitGrant := func(c *Client, want []servercfg.Account) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			m := c.link.Snapshot()
			got := map[string]bool{}
			for _, l := range m.Lines {
				if l.Allocated {
					got[l.Account] = true
				}
			}
			match := m.GrantNegotiated && !m.GrantChanging && len(got) == len(want) && usableAccounts(m) >= len(want)
			for _, a := range want {
				match = match && got[a.ID]
			}
			if match {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("grant mismatch: got %v want %v usable %d changing %v", got, len(want), usableAccounts(m), m.GrantChanging)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Ensure both standing reader pools are warm; otherwise a deliberately
	// unavailable initial account may legitimately be replaced.
	warmDeadline := time.Now().Add(15 * time.Second)
	for {
		warm := true
		for _, u := range cfg.Users {
			var row struct {
				Mail struct {
					Ready int `json:"ready_accounts"`
				}
			}
			raw, err := os.ReadFile(filepath.Join(root, "server", "mailstream-"+u.ID+".json"))
			if err != nil || json.Unmarshal(raw, &row) != nil || row.Mail.Ready != len(cfg.Accounts) {
				warm = false
			}
		}
		if warm {
			break
		}
		if time.Now().After(warmDeadline) {
			t.Fatal("server did not warm every account")
		}
		time.Sleep(100 * time.Millisecond)
	}
	first := connect(0)
	defer first.Close()
	waitGrant(first, cfg.Accounts[:8])
	second := connect(1)
	defer second.Close()
	waitGrant(first, cfg.Accounts[:8])
	waitGrant(second, cfg.Accounts[8:])
	transfer := func(c *Client, b byte) error {
		tr := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: httpsRoots}, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			stream, _, err := c.Open(ctx, wire.Connect{Host: "203.0.113.80", Port: 443})
			if err != nil {
				return nil, err
			}
			conn, ok := stream.(net.Conn)
			if !ok {
				stream.Close()
				return nil, errors.New("not a TCP connection")
			}
			return conn, nil
		}}
		defer tr.CloseIdleConnections()
		payload := bytes.Repeat([]byte{b}, 256<<10)
		res, err := (&http.Client{Transport: tr, Timeout: 15 * time.Second}).Post("https://example.com/", "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			return err
		}
		defer res.Body.Close()
		got, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 || !bytes.Equal(payload, got) {
			return fmt.Errorf("HTTPS integrity: %v", err)
		}
		return nil
	}
	results := make(chan error, 2)
	go func() { results <- transfer(first, 0x41) }()
	go func() { results <- transfer(second, 0x42) }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	first.Close()
	// Preserve the survivor's seven existing paths; add one unused account.
	deadline := time.Now().Add(40 * time.Second)
	for {
		m := second.link.Snapshot()
		if m.AllocatedAccounts == 8 && !m.GrantChanging && usableAccounts(m) >= 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("survivor did not regain baseline: %d/%d changing=%v", usableAccounts(m), m.AllocatedAccounts, m.GrantChanging)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := transfer(second, 0x43); err != nil {
		t.Fatal("survivor transfer", err)
	}
	if peak.Load() > 240 {
		t.Fatal("socket budget exceeded", peak.Load())
	}
	t.Logf("15 accounts: sequential 8/7 grants, independent verified HTTPS, survivor expansion to 8; peak=%d/240", peak.Load())
}
