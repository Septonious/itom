package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
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

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/transport"
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
		allowed := false
		for _, u := range cfg.Users {
			if u.ID == user.ID {
				allowed = true
			}
		}
		if !allowed || (target.Host != "field-test.fixture" && target.Host != "203.0.113.80") || target.Port != 443 {
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
	testPublicEndpoints(t, publicEndpointCase{})
}

func TestServerStartedBeforePhoneStillAcceptsClientAfterNinetySeconds(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{clientDelay: 91 * time.Second})
}

func TestPublicEndpointsAcceptProviderKeywordCaseChangesWithoutSpuriousRestart(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{caseChanges: true})
}

func TestProviderRequestRefusalAfterSuccessfulLoginsReportsRequestPhaseWithoutAppending(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{requestRefusal: true})
}

func TestProviderKeywordLengthLimitsStillAllowAuthenticatedStartupAndHTTPSRestart(t *testing.T) {
	for _, limit := range []int{64, 32, 16} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			testPublicEndpoints(t, publicEndpointCase{keywordBytes: limit, caseChanges: true})
		})
	}
}

func TestTwentySelectedLinesCarryHTTPSAcrossRestartsWithShortProviderKeywords(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{keywordBytes: 32, accounts: 10, rounds: 4})
}

func TestInPlaceUpgradeWithLegacyControlMailAndUnknownDataDebtStillCarriesHTTPS(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{legacy: true})
}

func TestFieldTopologyFifteenAccountsTwoUsersUpgradesAndRestartsWithoutClearingState(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{legacy: true, fieldTopology: true})
}

func TestNativeTUNAndSOCKSClientsCarryTLSAfterSuccessfulStartup(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{native: true, keywordBytes: 32, accounts: 10})
}

func TestSelectedLinesAndControlPermissionCannotReportReadyWhenDataPathIsBlackholed(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{dataBlackhole: true, keywordBytes: 32})
}

type publicEndpointCase struct {
	dataBlackhole                      bool
	native                             bool
	clientDelay                        time.Duration
	caseChanges, legacy, fieldTopology bool
	requestRefusal                     bool
	keywordBytes, accounts, rounds     int
}

func testPublicEndpoints(t *testing.T, scenario publicEndpointCase) {
	var nativeBinary string
	if scenario.native {
		nativeBinary = filepath.Join(t.TempDir(), "native-client.test")
		build := exec.Command("go", "test", "-c", "-race", "-o", nativeBinary, ".")
		build.Dir = "../../native"
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("native integration build: %v\n%s", err, out)
		}
	}
	clientDelay, caseChanges, legacy := scenario.clientDelay, scenario.caseChanges, scenario.legacy
	accountCount, userCount := 1, 1
	if scenario.accounts > 0 {
		accountCount = scenario.accounts
	}
	if scenario.fieldTopology {
		accountCount, userCount = 15, 2
	}
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
	nativeStopFile := filepath.Join(t.TempDir(), "native-close")
	allowShutdown := func() bool {
		if shutdown.Load() || scenario.dataBlackhole {
			return true
		}
		_, e := os.Stat(nativeStopFile)
		return scenario.native && e == nil
	}
	boxes := map[string]*sharedMailbox{upFolder: {keywords: true, reclamation: true, allowClose: allowShutdown}, downFolder: {keywords: true, reclamation: true, allowClose: allowShutdown}}
	// The old protocol used these same base folders for unwrapped control
	// packets. These are valid physical MIME messages, not v2 envelopes.
	if legacy {
		for _, folder := range []string{upFolder, downFolder} {
			raw, e := mailbox.Encode([]byte("ITOM legacy control packet from the previous installed transport"))
			if e != nil {
				t.Fatal(e)
			}
			boxes[folder].bodies = [][]byte{raw}
		}
	}
	for _, folder := range []string{upFolder, downFolder} {
		boxes[folder+"-session-v2"] = &sharedMailbox{keywords: true, reclamation: true, allowClose: allowShutdown, rejectRequests: scenario.requestRefusal, keywordBytes: scenario.keywordBytes, hideServerData: scenario.dataBlackhole}
	}
	// On an actual in-place upgrade the new namespace does not exist yet.
	created := map[string]bool{upFolder: true, downFolder: true}
	if !legacy {
		created[upFolder+"-session-v2"], created[downFolder+"-session-v2"] = true, true
	}
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
	// Account credentials and user mailboxes are independent, as on the field server.
	if scenario.fieldTopology {
		for _, folder := range []string{"itom-other-up", "itom-other-down"} {
			raw, e := mailbox.Encode([]byte("legacy control for second user"))
			if e != nil {
				t.Fatal(e)
			}
			boxes[folder] = &sharedMailbox{keywords: true, reclamation: true, allowClose: allowShutdown, bodies: [][]byte{raw}}
			boxes[folder+"-session-v2"] = &sharedMailbox{keywords: true, reclamation: true, allowClose: allowShutdown}
			created[folder] = true
		}
	}
	providerBoxes := map[string]map[string]*sharedMailbox{"fixture": boxes}
	for n := 2; n <= accountCount; n++ {
		username := fmt.Sprintf("fixture-%02d", n)
		clone := map[string]*sharedMailbox{}
		for folder, box := range boxes {
			clone[folder] = &sharedMailbox{keywords: true, reclamation: true, allowClose: allowShutdown, bodies: append([][]byte(nil), box.bodies...), flagCase: box.flagCase, keywordBytes: box.keywordBytes}
		}
		providerBoxes[username] = clone
	}
	providerCreated := map[string]map[string]bool{}
	for username := range providerBoxes {
		providerCreated[username] = map[string]bool{}
		for folder, exists := range created {
			providerCreated[username][folder] = exists
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
				loginFields := strings.Fields(line)
				if len(loginFields) != 4 {
					t.Error("invalid LOGIN")
					return
				}
				username := strings.Trim(loginFields[2], "\"")
				accountBoxes := providerBoxes[username]
				if accountBoxes == nil {
					t.Error("unknown account credentials")
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
				mu.Lock()
				exists := providerCreated[username][folder]
				mu.Unlock()
				if !exists {
					fmt.Fprintf(conn, "%s NO [TRYCREATE] absent\r\n", tag(line))
					line, err = r.ReadString('\n')
					if err != nil {
						return
					}
					if !strings.Contains(line, " CREATE \""+folder+"\"") {
						t.Error("expected creation of exact planned namespace")
						return
					}
					mu.Lock()
					already := providerCreated[username][folder]
					providerCreated[username][folder] = true
					mu.Unlock()
					if already {
						fmt.Fprintf(conn, "%s NO already exists\r\n", tag(line))
					} else {
						fmt.Fprintf(conn, "%s OK created\r\n", tag(line))
					}
					line, err = r.ReadString('\n')
					if err != nil {
						return
					}
					if !strings.Contains(line, " SELECT \""+folder+"\"") {
						t.Error("created folder was not verified with SELECT")
						return
					}
				}
				box := accountBoxes[folder]
				if box == nil {
					t.Error("unplanned folder")
					return
				}
				fmt.Fprintf(conn, "* OK [PERMANENTFLAGS (\\Seen \\*)] keywords\r\n* OK [UIDVALIDITY 9] selected\r\n%s OK selected\r\n", tag(line))
				conn.SetDeadline(time.Time{})
				var delays []time.Duration
				if scenario.fieldTopology || scenario.accounts > 1 {
					// Real stated client/provider and server/provider service range.
					// Keep all original OPEN/TLS/header/application deadlines.
					delays = []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 30 * time.Millisecond}
				}
				box.commandScript(t, folder, delays, true, false)(&bufferedConn{Conn: conn, reader: r})
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
	if scenario.fieldTopology {
		p.Transport.AccountSessions, p.Transport.EndpointConnections, p.Transport.Participants = 10, 60, 2
	}
	if accountCount > 1 {
		for n := 2; n <= accountCount; n++ {
			account := p.Accounts[0]
			account.ID = fmt.Sprintf("account%d", n)
			account.Mail.Username = fmt.Sprintf("fixture-%02d", n)
			p.Accounts = append(p.Accounts, account)
		}
	}
	cfg := servercfg.Config{Schema: 1, Server: p.Server, Provider: p.Provider, Users: []servercfg.User{p.User}, Accounts: p.Accounts, Transport: p.Transport}
	if scenario.fieldTopology {
		other := p.User
		other.ID = "87654321"
		other.Name = "other"
		other.Up = "itom-other-up"
		other.Down = "itom-other-down"
		other.Key = strings.Repeat("28", 32)
		cfg.Users = append(cfg.Users, other)
	}
	configPath := filepath.Join(root, "server.db")
	legacyUser := p.User.ID
	var legacyAttempt journal.AttemptRef
	legacyPath := filepath.Join(root, "server-state", "account1", p.User.ID, "data.db")
	legacyConfig := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: "account1", Provider: p.Accounts[0].Mail.Host, Limit: pool.Amount{Bytes: 12 << 20, Messages: 192}, Users: []journal.User{{ID: p.User.ID, Limit: pool.Amount{Bytes: 12 << 20, Messages: 192}, Folders: []string{upFolder, downFolder}}}}}}
	if legacy {
		if err := os.MkdirAll(filepath.Dir(legacyPath), 0700); err != nil {
			t.Fatal(err)
		}
		s, e := journal.Open(legacyPath, legacyConfig, time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.EnableAfterInventory("account1", p.User.ID, s.Run()); e != nil {
			t.Fatal(e)
		}
		// Match the field trace's almost-full retained ledger. The unspent
		// reservation also remains held: missing wallet history is no refund.
		g, e := s.Issue(journal.Scope{Account: "account1", User: p.User.ID, Session: wire.ID{91}}, pool.Amount{Bytes: 11938954, Messages: 181})
		if e != nil {
			t.Fatal(e)
		}
		a, e := s.PrepareStarted(g.Ref, downFolder, wire.ID{92}, 65536, []byte("legacy ciphertext with lost APPEND acknowledgement"))
		if e != nil {
			t.Fatal(e)
		}
		if e = s.Complete(a.Ref, journal.Uncertain); e != nil {
			t.Fatal(e)
		}
		legacyAttempt = a.Ref
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
	}
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
	cohort := max(2*userCount, scenario.rounds)
	// Scale only the outer cohort budget with the number of VPN restarts.
	// Per-attempt startup and all IMAP/OPEN/TLS/HTTP deadlines are unchanged.
	cmd := exec.Command(executable, "-test.run=^TestEndpointServerProcessHelper$", "-test.v", "-test.timeout="+(time.Duration(cohort)*45*time.Second/2+clientDelay).String())
	if profile := os.Getenv("ITOM_TEST_SERVER_CPU_PROFILE"); profile != "" {
		cmd.Args = append(cmd.Args, "-test.cpuprofile="+profile)
	}
	cmd.Env = append(os.Environ(), "ITOM_RC1_TEST_SERVER_CONFIG="+configPath, "ITOM_RC1_TEST_STATE="+filepath.Join(root, "server-state"), "ITOM_RC1_TEST_EXIT="+upstream.Listener.Addr().String(), "SSL_CERT_FILE="+rootFile)
	var logs lockedTestBuffer
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
		if legacy {
			next := legacyConfig
			next.Accounts[0].Users[0].Folders = []string{upFolder, downFolder, upFolder + "-session-v2", downFolder + "-session-v2"}
			s, e := journal.OpenCompatible(legacyPath, next, []journal.Config{legacyConfig}, time.Second)
			if e != nil {
				t.Error("original journal not preserved", e)
				return
			}
			defer s.Close()
			a, e := s.GetAttempt(legacyAttempt)
			if e != nil || a.State != journal.Unknown {
				t.Error("upgrade forgot unknown historical APPEND", a, e)
			}
			held, e := s.Held("account1", legacyUser)
			if e != nil || held.Bytes < 11938954 || held.Messages < 181 {
				t.Error("upgrade refunded historical debt", held, e)
			}
			for _, folder := range []string{upFolder, downFolder} {
				if len(boxes[folder].bodies) != 1 {
					t.Error("upgrade touched old control mail", folder)
				}
			}
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cohort)*15*time.Second)
	defer cancel()
	certPool := x509.NewCertPool()
	certPool.AddCert(upstream.Certificate())
	for round := 0; round < cohort; round++ {
		p.User = cfg.Users[round%userCount]
		clientDir := filepath.Join(root, "client-state", p.User.ID)
		if scenario.native {
			raw, e := json.Marshal(map[string]any{"Profile": p, "DNS": []string{"127.0.0.1"}, "Socks": map[string]any{"Port": 19081}, "Diagnostics": true})
			if e != nil {
				t.Fatal(e)
			}
			input := filepath.Join(root, "native-config.json")
			if e = os.WriteFile(input, raw, 0600); e != nil {
				t.Fatal(e)
			}
			certFile := filepath.Join(root, "https-root.pem")
			if e = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); e != nil {
				t.Fatal(e)
			}
			run := exec.Command(nativeBinary, "-test.run=^TestRealNativeClientProcessHelper$", "-test.v", "-test.timeout=20s")
			os.Remove(nativeStopFile)
			run.Env = append(os.Environ(), "ITOM_NATIVE_TEST_STOP="+nativeStopFile, "ITOM_NATIVE_TEST_EVIDENCE="+filepath.Join(root, "native-evidence.json"), "ITOM_NATIVE_TEST_CONFIG="+input, "ITOM_NATIVE_TEST_STATE="+clientDir, "SSL_CERT_FILE="+rootFile, "ITOM_NATIVE_TEST_HTTPS_ROOT="+certFile)
			if out, e := run.CombinedOutput(); e != nil {
				if snapshot, e := os.ReadFile(filepath.Join(root, "server-state", "v2-"+p.User.ID+".json")); e == nil {
					t.Log("native failed server state", string(snapshot))
				}
				t.Fatalf("native traffic round=%d err=%v\n%s\nserver=%s", round, e, out, logs.String())
			} else {
				t.Logf("native round=%d\n%s", round, out)
			}
			continue
		}
		var progress []tunnel.StartupProgress
		client, err := tunnel.StartClientWithOptions(ctx, p, clientDir, &net.Dialer{}, tunnel.ClientOptions{Diagnostics: true, TLSConfig: &tls.Config{RootCAs: roots}, Progress: func(p tunnel.StartupProgress) { progress = append(progress, p) }})
		if scenario.dataBlackhole {
			shutdown.Store(true)
			if client != nil || err == nil || err.Error() != "V2_STARTUP_PATH" {
				if client != nil {
					client.Close()
				}
				t.Fatal("control-only readiness admitted a broken DATA path", err)
			}
			for _, p := range progress {
				if p.Stage == "READY" {
					t.Fatal("READY emitted without DATA echo", p)
				}
			}
			last := progress[len(progress)-1]
			if last.Stage != "PATH" || last.SelectedLines != 2 || last.TotalLines != 2 {
				t.Fatal("DATA path failure lost actual selected lines", last)
			}
			return
		}
		if scenario.requestRefusal {
			shutdown.Store(true)
			if client != nil || err == nil || err.Error() != "V2_STARTUP_REQUEST" || !errors.Is(err, transport.ErrRefused) {
				t.Fatal("provider refusal was hidden as offer discovery or produced a session", err)
			}
			last := progress[len(progress)-1]
			if last.Stage != "REQUEST" || last.FailureReason != "IMAP_TAGGED_REFUSAL" || last.SelectedLines != 2 || last.TotalLines != 2 {
				t.Fatal("final failure lost phase, reason or actual SELECT count", last)
			}
			box := boxes[upFolder+"-session-v2"]
			box.mu.Lock()
			physicalBodies := len(box.bodies)
			box.mu.Unlock()
			if physicalBodies != 1 {
				t.Fatal("refused request appended an invitation or an uncharged client HELLO", physicalBodies)
			}
			return
		}
		if err != nil {
			t.Log("failed actual startup progress", progress)
			if raw, e := os.ReadFile(filepath.Join(root, "server-state", "v2-"+p.User.ID+".json")); e == nil {
				t.Log("failed actual startup server state", string(raw))
			}
			t.Fatal("public API startup", round, err)
		}
		if len(progress) == 0 || progress[0].Stage != "PROFILE" || progress[len(progress)-1].Stage != "READY" {
			t.Fatal("startup did not report real phase boundaries", progress)
		}
		lastSelected := 0
		for _, p := range progress {
			if p.TotalLines != 2*accountCount || p.SelectedLines < lastSelected || p.SelectedLines > 2*accountCount {
				t.Fatal("invalid selected socket progress", p)
			}
			lastSelected = p.SelectedLines
		}
		if lastSelected != 2*accountCount {
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
			t.Log("failed actual HTTP diagnostics", client.Diagnostics())
			if raw, e := os.ReadFile(filepath.Join(root, "server-state", "v2-"+p.User.ID+".json")); e == nil {
				t.Log("failed actual HTTP server diagnostics", string(raw))
			}
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
		if client.Stats().TotalLines != 2*accountCount || client.Stats().ActiveLines != 2*accountCount {
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
	if scenario.keywordBytes > 0 {
		box := boxes[upFolder+"-session-v2"]
		box.mu.Lock()
		defer box.mu.Unlock()
		if box.keywordRefusals == 0 || box.peakFlags > 20 {
			t.Fatal("fixture did not reject long names or request metadata accumulated", box.keywordRefusals, box.peakFlags)
		}
		for _, flags := range box.flags {
			for flag := range flags {
				if strings.HasPrefix(flag, "itom2_") || (len(flag) == 16 || len(flag) == 31 || len(flag) == 59) && strings.HasPrefix(flag, "i") {
					t.Fatal("authenticated request metadata was not cleared", flag)
				}
			}
		}
	}
}

// Child stdout can still be appended when a failed request snapshots logs.
// The test's failure reporter must not introduce a second, unrelated data race.
type lockedTestBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedTestBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
