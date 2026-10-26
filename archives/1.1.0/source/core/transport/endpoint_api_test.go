package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"itom.local/core/tunnel"
	"itom.local/core/wire"
)

// A fresh process uses its actual system TLS root loader with a test-only local
// CA. Production ServeManaged and StartClientWithOptions execute unchanged;
// no selected executor, keys, inventory enable or wallet offer is injected.
func TestEndpointServerProcessHelper(t *testing.T) {
	configPath := os.Getenv("ITOM_RC1_TEST_SERVER_CONFIG")
	if configPath == "" {
		return
	}
	cfg, err := servercfg.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	address := os.Getenv("ITOM_RC1_TEST_EXIT")
	err = tunnel.ServeManaged(ctx, cfg, os.Getenv("ITOM_RC1_TEST_STATE"), func(code string) { t.Log(code) }, func(ctx context.Context, user servercfg.User, target wire.Connect) (*net.TCPConn, bool, error) {
		if user.ID != cfg.Users[0].ID || target.Host != "field-test.fixture" || target.Port != 443 {
			return nil, true, errors.New("fixture exit denied")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err != nil {
			return nil, true, err
		}
		return conn.(*net.TCPConn), true, nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPublicClientAndServerAPIsCarryTLSAndRestartWithOriginalWalletPaths(t *testing.T) {
	testPublicEndpoints(t, 0, false)
}

func TestServerStartedBeforePhoneStillAcceptsClientAfterNinetySeconds(t *testing.T) {
	testPublicEndpoints(t, 91*time.Second, false)
}

func TestPublicEndpointsAcceptProviderKeywordCaseChangesWithoutSpuriousRestart(t *testing.T) {
	testPublicEndpoints(t, 0, true)
}

func testPublicEndpoints(t *testing.T, clientDelay time.Duration, caseChanges bool) {
	t.Helper()
	if os.Getenv("ITOM_RC1_TEST_SERVER_CONFIG") != "" {
		t.Fatal("helper scope escaped")
	}
	cert, roots, err := certificate()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	var peers sync.WaitGroup
	var mu sync.Mutex
	active := map[net.Conn]bool{}
	upFolder, downFolder := "itom-field-up", "itom-field-down"
	var shutdown atomic.Bool
	boxes := map[string]*sharedMailbox{upFolder: {keywords: true, reclamation: true, allowClose: shutdown.Load}, downFolder: {keywords: true, reclamation: true, allowClose: shutdown.Load}}
	if caseChanges {
		var count atomic.Uint64
		for _, box := range boxes {
			box.flagCase = func(flag string) string {
				if count.Add(1)%2 == 0 {
					return strings.ToUpper(flag)
				}
				return strings.ToLower(flag)
			}
		}
	}
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			active[conn] = true
			mu.Unlock()
			peers.Go(func() {
				defer conn.Close()
				defer func() { mu.Lock(); delete(active, conn); mu.Unlock() }()
				conn.SetDeadline(time.Now().Add(30 * time.Second))
				r := bufio.NewReader(conn)
				fmt.Fprint(conn, "* OK field provider\r\n")
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if !strings.Contains(line, " LOGIN ") {
					t.Error("missing real LOGIN")
					return
				}
				fmt.Fprintf(conn, "%s OK login\r\n", tag(line))
				line, err = r.ReadString('\n')
				if err != nil {
					return
				}
				if !strings.Contains(line, " CAPABILITY") {
					t.Error("missing real CAPABILITY")
					return
				}
				fmt.Fprintf(conn, "* CAPABILITY IMAP4rev1 IDLE UIDPLUS\r\n%s OK capability\r\n", tag(line))
				line, err = r.ReadString('\n')
				if err != nil {
					return
				}
				fields := strings.Fields(line)
				if len(fields) != 3 || fields[1] != "SELECT" {
					t.Error("missing real SELECT")
					return
				}
				folder := strings.Trim(fields[2], "\"")
				box := boxes[folder]
				if box == nil {
					t.Error("unplanned folder")
					return
				}
				fmt.Fprintf(conn, "* OK [PERMANENTFLAGS (\\Seen \\*)] keywords\r\n* OK [UIDVALIDITY 9] selected\r\n%s OK selected\r\n", tag(line))
				conn.SetDeadline(time.Time{})
				box.commandScript(t, folder, nil, true, false)(&bufferedConn{Conn: conn, reader: r})
			})
		}
	}()
	defer func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for conn := range active {
			conn.Close()
		}
		mu.Unlock()
		peers.Wait()
	}()
	root := t.TempDir()
	rootFile := filepath.Join(root, "fixture-root.pem")
	if err = os.WriteFile(rootFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	address := netip.MustParseAddrPort(listener.Addr().String())
	p := servercfg.Profile{Schema: 1, Kind: "itom-imap-probe", Server: wire.ID{1}, Provider: "fixture", User: servercfg.User{ID: "12345678", Name: "field", Enabled: true, Key: strings.Repeat("17", 32), Up: upFolder, Down: downFolder}, Accounts: []servercfg.Account{{ID: "account1", Mail: mailbox.Config{Host: "fixture.local", DialIP: "127.0.0.1", Port: int(address.Port()), TLS: "tls", Username: "fixture", Password: "fixture-only"}}}, Transport: servercfg.Transport{AccountSessions: 5, EndpointConnections: 30, Participants: 1}}
	cfg := servercfg.Config{Schema: 1, Server: p.Server, Provider: p.Provider, Users: []servercfg.User{p.User}, Accounts: p.Accounts, Transport: p.Transport}
	configPath := filepath.Join(root, "server.db")
	if err = servercfg.Update(configPath, func(c *servercfg.Config) error { *c = cfg; return nil }); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "request read", 500)
			return
		}
		w.Write(raw)
	}))
	defer upstream.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestEndpointServerProcessHelper$", "-test.v", "-test.timeout="+(45*time.Second+clientDelay).String())
	cmd.Env = append(os.Environ(), "ITOM_RC1_TEST_SERVER_CONFIG="+configPath, "ITOM_RC1_TEST_STATE="+filepath.Join(root, "server-state"), "ITOM_RC1_TEST_EXIT="+upstream.Listener.Addr().String(), "SSL_CERT_FILE="+rootFile)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cmd.Process.Signal(os.Interrupt)
		if err := cmd.Wait(); err != nil {
			t.Error("server process failed", err, logs.String())
		}
	}()
	if clientDelay > 0 {
		// Measure the real production timeout from actual rendezvous startup,
		// rather than assuming that launching the child means it is listening.
		ready := time.NewTimer(5 * time.Second)
		defer ready.Stop()
		for {
			raw, _ := os.ReadFile(filepath.Join(root, "server-state", "v2-"+p.User.ID+".json"))
			if bytes.Contains(raw, []byte(`"Stage":"waiting_request"`)) {
				break
			}
			select {
			case <-ready.C:
				t.Fatal("server did not reach standing rendezvous")
			case <-time.After(10 * time.Millisecond):
			}
		}
		t.Log("server listening; phone will connect after", clientDelay)
		timer := time.NewTimer(clientDelay)
		defer timer.Stop()
		<-timer.C
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clientDir := filepath.Join(root, "client-state")
	certPool := x509.NewCertPool()
	certPool.AddCert(upstream.Certificate())
	for round := 0; round < 2; round++ {
		var progress []tunnel.StartupProgress
		client, err := tunnel.StartClientWithOptions(ctx, p, clientDir, &net.Dialer{}, tunnel.ClientOptions{Diagnostics: true, TLSConfig: &tls.Config{RootCAs: roots}, Progress: func(p tunnel.StartupProgress) { progress = append(progress, p) }})
		if err != nil {
			t.Fatal("public API startup", round, err)
		}
		if len(progress) == 0 || progress[0].Stage != "PROFILE" || progress[len(progress)-1].Stage != "READY" {
			t.Fatal("startup did not report real phase boundaries", progress)
		}
		lastSelected := 0
		for _, p := range progress {
			if p.TotalLines != 2 || p.SelectedLines < lastSelected || p.SelectedLines > 2 {
				t.Fatal("invalid selected socket progress", p)
			}
			lastSelected = p.SelectedLines
		}
		if lastSelected != 2 {
			t.Fatal("selected sockets were not reported", progress)
		}
		shutdown.Store(false)
		tr := &http.Transport{DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			stream, _, err := client.Open(ctx, wire.Connect{Host: "field-test.fixture", Port: 443})
			if err != nil {
				return nil, err
			}
			return stream.(net.Conn), nil
		}, TLSClientConfig: &tls.Config{RootCAs: certPool, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond}
		hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		payload := bytes.Repeat([]byte{byte(round + 1)}, 4096)
		resp, err := hc.Post(upstream.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			client.Close()
			t.Fatal("public API actual TLS", round, err)
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		tr.CloseIdleConnections()
		if err != nil || !bytes.Equal(got, payload) {
			client.Close()
			t.Fatal("public API integrity", round, len(got), err)
		}
		if client.Stats().TotalLines != 2 {
			t.Fatal("unexpected selected socket count", client.Stats())
		}
		if _, err = os.Stat(filepath.Join(clientDir, "account1", "wallet.db")); err != nil {
			t.Fatal("original DATA wallet path not used", err)
		}
		if !strings.Contains(client.Diagnostics(), tunnel.TransportRevision) || strings.Contains(client.Diagnostics(), p.User.Key) {
			t.Fatal("diagnostics missing identity or exposed key")
		}
		trace, err := os.ReadFile(filepath.Join(root, "server-state", "transport-trace.jsonl"))
		if err != nil || !bytes.Contains(trace, []byte(`"Stage":"rendezvous"`)) || !bytes.Contains(trace, []byte(`"Stage":"imap_selected"`)) {
			t.Fatal("production server did not record real startup stages", err)
		}
		if bytes.Contains(trace, []byte(p.User.Key)) || bytes.Contains(trace, []byte("fixture-only")) {
			t.Fatal("server trace exposed private input")
		}
		shutdown.Store(true)
		_ = client.Close()
	}
}
