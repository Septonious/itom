package transport_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
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

	bolt "go.etcd.io/bbolt"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/transport"
	"itom.local/core/tunnel"
	"itom.local/core/wallet"
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
		logOriginalJournalCapacity(t, os.Getenv("ITOM_RC1_TEST_STATE"))
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

func TestStandingServerKeepsSelectedMailboxesAliveBeforePhoneArrives(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{clientDelay: 25 * time.Second, idleTimeout: 20 * time.Second, accounts: 3, keywordBytes: 32})
}

func TestExhaustedFirstControlAndSplitDataCapacityStillCarryHTTPSWithoutDebtReset(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{accounts: 3, splitCapacity: true, keywordBytes: 32})
}

func TestTwentyVPNRestartsReclaimConfirmedStartupTraffic(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{accounts: 3, rounds: 20, keywordBytes: 32})
}

func TestRetainedAuthenticatedMailboxBacklogCannotHideTheFirstDataRoundtrip(t *testing.T) {
	testPublicEndpoints(t, publicEndpointCase{accounts: 3, retainedBacklog: 128, keywordBytes: 32})
}

type publicEndpointCase struct {
	retainedBacklog                    int
	splitCapacity                      bool
	idleTimeout                        time.Duration
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
		boxes[folder+"-session-v2"] = &sharedMailbox{keywords: true, reclamation: true, allowClose: allowShutdown, rejectRequests: scenario.requestRefusal, keywordBytes: scenario.keywordBytes, hideServerData: scenario.dataBlackhole, idleTimeout: scenario.idleTimeout}
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
			clone[folder] = &sharedMailbox{keywords: true, reclamation: true, allowClose: allowShutdown, bodies: append([][]byte(nil), box.bodies...), flagCase: box.flagCase, keywordBytes: box.keywordBytes, idleTimeout: box.idleTimeout}
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
	// This long cohort retains the original 128 CONTROL + 192 DATA ceilings.
	// A provider folder serves both ledgers; the ordinary small fixtures keep 64.
	if scenario.rounds >= 20 {
		for _, folders := range providerBoxes {
			for _, box := range folders {
				box.messageLimit = 128 + 192
			}
		}
	}
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
	if scenario.splitCapacity {
		// Model the supplied full CONTROL ledger and missing old wallet
		// history. DATA is available only on account1; CONTROL only elsewhere.
		// These are original remote reservations, never refundable as zero.
		for n, a := range p.Accounts {
			name, amount := "control", pool.Amount{Bytes: 1 << 20, Messages: 128}
			if n > 0 {
				name, amount = "data", pool.Amount{Bytes: 3 << 20, Messages: 192}
			}
			limit := pool.Amount{Bytes: 2 << 20, Messages: 128}
			if name == "data" {
				limit = pool.Amount{Bytes: 12 << 20, Messages: 192}
			}
			config := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: limit, Users: []journal.User{{ID: p.User.ID, Limit: limit, Folders: []string{upFolder, downFolder, upFolder + "-session-v2", downFolder + "-session-v2"}}}}}}
			path := filepath.Join(root, "server-state", a.ID, p.User.ID, name+".db")
			if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			store, e := journal.Open(path, config, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if e = store.EnableAfterInventory(a.ID, p.User.ID, store.Run()); e != nil {
				t.Fatal(e)
			}
			if _, e = store.IssueRemote(journal.Scope{Account: a.ID, User: p.User.ID, Session: wire.ID{77}}, amount); e != nil {
				t.Fatal(e)
			}
			store.Close()
			// Registered before the server-process defer: verify after it stops.
			t.Cleanup(func() {
				s, e := journal.Open(path, config, time.Second)
				if e != nil {
					t.Error(e)
					return
				}
				defer s.Close()
				held, e := s.Held(a.ID, p.User.ID)
				if e != nil || held.Messages < amount.Messages || held.Bytes < amount.Bytes {
					t.Error("old unknown reservation was refunded", held, e)
				}
				got, e := s.Limit(a.ID, p.User.ID)
				if e != nil || got != limit {
					t.Error("quota was increased", got, e)
				}
			})
		}
	}
	if scenario.retainedBacklog > 0 {
		oldEpoch, _ := sessionEpochs(t, wire.ID{77})
		key, e := hex.DecodeString(p.User.Key)
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range p.Accounts {
			limit := pool.Amount{Bytes: 12 << 20, Messages: 192}
			jc := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: limit, Users: []journal.User{{ID: p.User.ID, Limit: limit, Folders: []string{upFolder, downFolder, upFolder + "-session-v2", downFolder + "-session-v2"}}}}}}
			path := filepath.Join(root, "server-state", a.ID, p.User.ID, "data.db")
			if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
				t.Fatal(e)
			}
			store, e := journal.Open(path, jc, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if e = store.EnableAfterInventory(a.ID, p.User.ID, store.Run()); e != nil {
				t.Fatal(e)
			}
			scope := journal.Scope{Account: a.ID, User: p.User.ID, Session: wire.ID{77}}
			folder := upFolder + "-session-v2"
			shape := wire.QuotaKey{Store: store.StoreID(), Epoch: store.Run(), Grant: 1, Account: a.ID, User: p.User.ID, Session: scope.Session}
			for n := 0; n < scenario.retainedBacklog; n++ {
				// Real original local tickets and MAC-authenticated retained envelopes.
				// Old opaque ciphertext must never enter the new Noise session.
				packet := bytes.Repeat([]byte{byte(n + 1)}, 64)
				ticket, _, e := store.ReserveLocalSized(scope, folder, len(packet), len(packet), func(size int) (uint64, error) {
					body, e := wire.ServerEnvelopeSize(shape, wire.ServerData, folder, size)
					if e != nil {
						return 0, e
					}
					return mailbox.EncodedSize(body)
				})
				if e != nil {
					t.Fatal(e)
				}
				batch := wire.ID{byte(n + 1), 99}
				usage, body, e := wire.BindServerEnvelope(ticket.Key(), wire.ServerData, batch, folder, packet, key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
				if e != nil {
					t.Fatal(e)
				}
				attempt, e := ticket.Commit(batch, usage.Attempt.EncodedBytes, body)
				if e != nil {
					t.Fatal(e)
				}
				mime, e := mailbox.Encode(body)
				if e != nil {
					t.Fatal(e)
				}
				box := providerBoxes[a.Mail.Username][folder]
				box.messageLimit = 128 + 192 // original separate CONTROL and DATA ledgers share this FAST folder
				box.bodies = append(box.bodies, mime)
				box.bytes += len(mime)
				if e = store.Complete(attempt.Ref, journal.Accepted); e != nil {
					t.Fatal(e)
				}
			}
			before, e := store.Held(a.ID, p.User.ID)
			if e != nil {
				t.Fatal(e)
			}
			store.Close()
			// Surviving original wallet history creates the field-sized recovery
			// fence/status backlog; no fixture grants enter the new live epoch.
			cc := journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: pool.Amount{Bytes: 2 << 20, Messages: 128}, Users: []journal.User{{ID: p.User.ID, Limit: pool.Amount{Bytes: 2 << 20, Messages: 128}, Folders: []string{upFolder, downFolder, upFolder + "-session-v2", downFolder + "-session-v2"}}}}}}
			cs, e := journal.Open(filepath.Join(root, "server-state", a.ID, p.User.ID, "control.db"), cc, time.Second)
			if e != nil {
				t.Fatal(e)
			}
			if e = cs.EnableAfterInventory(a.ID, p.User.ID, cs.Run()); e != nil {
				t.Fatal(e)
			}
			wp := filepath.Join(root, "client-state", p.User.ID, a.ID, "control-wallet.db")
			if e = os.MkdirAll(filepath.Dir(wp), 0700); e != nil {
				t.Fatal(e)
			}
			cw, e := wallet.OpenBound(wp, cs.StoreID(), oldEpoch)
			if e != nil {
				t.Fatal(e)
			}
			for range 32 {
				g, e := cs.IssueRemote(scope, pool.Amount{Bytes: 4096, Messages: 1})
				if e != nil {
					t.Fatal(e)
				}
				offer, e := cs.RemoteOffer(g.Ref)
				if e != nil {
					t.Fatal(e)
				}
				if e = cw.Accept(offer); e != nil {
					t.Fatal(e)
				}
			}
			cw.Close()
			cs.Close()
			t.Cleanup(func() {
				s, e := journal.Open(path, jc, time.Second)
				if e != nil {
					t.Error(e)
					return
				}
				defer s.Close()
				after, e := s.Held(a.ID, p.User.ID)
				if e != nil || after.Bytes < before.Bytes || after.Messages < before.Messages {
					t.Error("retained-history optimization refunded original debt", before, after, e)
				}
				if e = s.Audit(); e != nil {
					t.Error(e)
				}
			})
		}
	}

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
	serverCohortLifetime := time.Duration(cohort)*45*time.Second/2 + clientDelay
	if scenario.retainedBacklog > 0 {
		serverCohortLifetime = time.Duration(cohort)*90*time.Second + clientDelay + 15*time.Second
	}
	cmd := exec.Command(executable, "-test.run=^TestEndpointServerProcessHelper$", "-test.v", "-test.timeout="+serverCohortLifetime.String())
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
	cohortBudget := time.Duration(cohort) * 15 * time.Second
	if scenario.retainedBacklog > 0 {
		cohortBudget = time.Duration(cohort) * 90 * time.Second
	} // outer cohort only; original 90s startup and all request deadlines remain unchanged
	ctx, cancel := context.WithTimeout(context.Background(), cohortBudget)
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
		clientBegin := time.Now()
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
		if scenario.retainedBacklog > 0 {
			t.Logf("cold_start_session=%d elapsed_ms=%d measured_data_rtt_ms=%d", round, time.Since(clientBegin).Milliseconds(), client.Probe().RTTMillis)
		}
		if scenario.rounds >= 20 {
			t.Logf("persistent_restart=%d measured_data_rtt_ms=%d", round, client.Probe().RTTMillis)
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
		var trace []byte
		// The full field topology can legitimately rotate the bounded trace.
		// Check retained evidence, not just whichever file is currently active.
		for _, suffix := range []string{".2", ".1", ""} {
			raw, e := os.ReadFile(filepath.Join(root, "server-state", "transport-trace.jsonl"+suffix))
			if e != nil && !os.IsNotExist(e) {
				t.Fatal("reading rotated server trace", e)
			}
			trace = append(trace, raw...)
		}
		if !bytes.Contains(trace, []byte(`"Stage":"rendezvous"`)) || !bytes.Contains(trace, []byte(`"Stage":"imap_selected"`)) {
			t.Fatal("production server did not retain real startup stages")
		}
		if bytes.Contains(trace, []byte(p.User.Key)) || bytes.Contains(trace, []byte("fixture-only")) {
			t.Fatal("server trace exposed private input")
		}
		shutdown.Store(true)
		_ = client.Close()
	}
	if scenario.keywordBytes > 0 {
		refusals := 0
		for _, accountBoxes := range providerBoxes {
			for folder, box := range accountBoxes {
				if !strings.HasSuffix(folder, "-session-v2") {
					continue
				}
				box.mu.Lock()
				refusals += box.keywordRefusals
				if box.peakFlags > 20 {
					box.mu.Unlock()
					t.Fatal("request metadata accumulated", folder, box.peakFlags)
				}
				for _, flags := range box.flags {
					for flag := range flags {
						if strings.HasPrefix(flag, "itom2_") || (len(flag) == 16 || len(flag) == 31 || len(flag) == 59) && strings.HasPrefix(flag, "i") {
							box.mu.Unlock()
							t.Fatal("authenticated request metadata was not cleared", folder, flag)
						}
					}
				}
				box.mu.Unlock()
			}
		}
		if refusals == 0 {
			t.Fatal("fixture did not reject long request names")
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

// Read-only failure evidence after ServeManaged has closed its original stores.
// Only synthetic test counters are logged, never config, keys or mail bodies.
func logOriginalJournalCapacity(t *testing.T, root string) {
	t.Helper()
	filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || (entry.Name() != "control.db" && entry.Name() != "data.db") {
			return nil
		}
		db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Millisecond})
		if err != nil {
			return nil
		}
		defer db.Close()
		db.View(func(tx *bolt.Tx) error {
			meta := tx.Bucket([]byte("meta"))
			if meta == nil {
				return nil
			}
			counts := map[string]uint64{}
			for _, key := range []string{"grant_count", "attempt_count", "last_grant", "last_attempt"} {
				raw := meta.Get([]byte(key))
				if len(raw) == 8 {
					counts[key] = binary.BigEndian.Uint64(raw)
				}
			}
			var grants, attempts []map[string]any
			for _, item := range []struct {
				name string
				out  *[]map[string]any
			}{{"grants", &grants}, {"attempts", &attempts}} {
				bucket := tx.Bucket([]byte(item.name))
				if bucket == nil {
					continue
				}
				bucket.ForEach(func(_, raw []byte) error {
					var row map[string]any
					if json.Unmarshal(raw, &row) == nil {
						*item.out = append(*item.out, row)
					}
					return nil
				})
			}
			var limit, heldBytes, heldMessages uint64
			for _, g := range grants {
				amount, _ := g["Limit"].(map[string]any)
				released, _ := g["Released"].(map[string]any)
				if b, ok := amount["Bytes"].(float64); ok {
					heldBytes += uint64(b)
				}
				if b, ok := released["Bytes"].(float64); ok {
					heldBytes -= uint64(b)
				}
				if m, ok := amount["Messages"].(float64); ok {
					heldMessages += uint64(m)
				}
				if m, ok := released["Messages"].(float64); ok {
					heldMessages -= uint64(m)
				}
			}
			var cfg journal.Config
			if json.Unmarshal(meta.Get([]byte("config")), &cfg) == nil && len(cfg.Accounts) > 0 {
				limit = cfg.Accounts[0].Limit.Messages
			}
			relative, _ := filepath.Rel(root, path)
			t.Logf("original_journal_capacity path=%s counts=%v held_bytes=%d held_messages=%d original_message_limit=%d attempts=%d", relative, counts, heldBytes, heldMessages, limit, len(attempts))
			return nil
		})
		return nil
	})
}
