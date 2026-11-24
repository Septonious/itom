//go:build linux

package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/servercfg"
	"itom.local/core/wire"
)

// Uses the same verified-TLS fixture as the native-process integration test.
// Independent users share one physical mailbox and obey its 16-socket limit.
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
		if s := j.c.Snapshot(); s.ActiveLines != 4 || s.TotalLines != 4 {
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
	if peak.Load() > 16 {
		t.Fatal("provider connection budget exceeded", peak.Load())
	}
	t.Logf("two simultaneous users: 4/4 lines each, HTTPS integrity, exit isolation, independent disconnect; peak %d/16", peak.Load())
}
