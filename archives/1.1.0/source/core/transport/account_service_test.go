package transport_test

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
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/qualification"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func accountIssuer(t *testing.T, path, account string, folders []string) *journal.Store {
	t.Helper()
	limit := pool.Amount{Bytes: 64 << 20, Messages: 128}
	cfg := journal.Config{Accounts: []journal.Account{{ID: account, Provider: "fixture", Limit: limit, Users: []journal.User{{ID: "12345678", Limit: limit, Folders: folders}}}}, MaxGrants: 128, MaxAttempts: 256}
	s, err := journal.Open(path, cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Trusted fixture inventory only; production must perform the real inventory.
	if err = s.EnableAfterInventory(account, "12345678", s.Run()); err != nil {
		t.Fatal(err)
	}
	return s
}

type accountRosterPair struct {
	f              *bootstrapFixture
	client, server *transport.RosterResult
	journals       []transport.RosterJournals
}

func distinctAccountRoster(t *testing.T, n int) *accountRosterPair {
	return distinctAccountRosterWithService(t, n, nil, nil)
}
func distinctAccountRosterWithService(t *testing.T, n int, clientService, serverService []time.Duration) *accountRosterPair {
	return distinctAccountRosterOnNetwork(t, n, clientService, serverService, nil, nil)
}
func distinctAccountRosterOnNetwork(t *testing.T, n int, clientService, serverService []time.Duration, clientNetwork, serverNetwork transport.NetworkDialer) *accountRosterPair {
	t.Helper()
	f := requestedInvitationNetworkFixture(t, clientService, serverService, true, clientNetwork, serverNetwork)
	ch, sh := exchangeDrivenBootstrap(t, f)
	plan := transport.RosterPlan{ClientBudget: transport.RuntimeBudget{Connections: 2 * n, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 3 * n, PerAccount: 3}}
	var cl, sl []transport.RosterLane
	var paths []transport.RosterWalletPaths
	type pendingLane struct {
		index   int
		client  bool
		network transport.NetworkDialer
		open    func(context.Context, transport.NetworkDialer) (*transport.Executor, error)
	}
	var pending []pendingLane
	var journals []transport.RosterJournals
	root := t.TempDir()
	for index := 1; index <= n; index++ {
		account := fmt.Sprintf("account%d", index)
		dir := filepath.Join(root, account, "12345678")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		control := f.store
		if index != 1 {
			control = accountIssuer(t, filepath.Join(dir, "control.db"), account, []string{"fast"})
		}
		folders := []string{"fast", "bulk"}
		for k := range 8 {
			folders = append(folders, fmt.Sprintf("retained-%d", k))
		}
		data := accountIssuer(t, filepath.Join(dir, "data.db"), account, folders)
		journals = append(journals, transport.RosterJournals{Account: account, Control: control, Data: data})
		wallets := transport.RosterWalletPaths{Account: account, Data: filepath.Join(dir, "data-wallet.db")}
		if index != 1 {
			wallets.Control = filepath.Join(dir, "control-wallet.db")
		}
		paths = append(paths, wallets)
		if index == 2 {
			// Original old-session writer history survives in its actual DATA file.
			old, err := wallet.Open(wallets.Data, "12345678", data.StoreID())
			if err != nil {
				t.Fatal(err)
			}
			g, err := data.IssueRemote(journal.Scope{Account: account, User: "12345678", Session: old.Session()}, pool.Amount{Bytes: 8192, Messages: 4})
			if err != nil {
				t.Fatal(err)
			}
			offer, err := data.RemoteOffer(g.Ref)
			if err != nil {
				t.Fatal(err)
			}
			if err = old.Accept(offer); err != nil {
				t.Fatal(err)
			}
			if _, _, err = old.SpendEnvelope(offer.Key, "retained-7", []byte("original unknown old-session bytes"), f.key, func([]byte) (uint64, error) { return 1024, nil }); err != nil {
				t.Fatal(err)
			}
			if err = old.Close(); err != nil {
				t.Fatal(err)
			}
		}
		plan.Accounts = append(plan.Accounts, transport.RosterAccount{Account: account, FastFolder: "fast", BulkFolder: "bulk"})
		for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
			folder := "fast"
			if plane == transport.Bulk {
				folder = "bulk"
			}
			cx, sx := f.cx, f.sx
			if index != 1 || plane != transport.Fast {
				box := &sharedMailbox{keywords: true, reclamation: true}
				cx, sx = nil, nil
				pending = append(pending, pendingLane{len(cl), true, clientNetwork, box.prepareAccountService(t, account, folder, clientService, true, false)}, pendingLane{len(sl), false, serverNetwork, box.prepareAccountService(t, account, folder, serverService, true, false)})
			}
			cl = append(cl, transport.RosterLane{Plane: plane, Executor: cx})
			sl = append(sl, transport.RosterLane{Plane: plane, Executor: sx})
		}
	}
	// Setup is bounded and parallel across the already listed lanes. Serial
	// setup under a real path delay can consume the fixture's original 4s idle
	// guard on early sockets before the rest of the roster is selected.
	jobs := make(chan pendingLane, len(pending))
	failed := make(chan error, len(pending))
	for _, lane := range pending {
		jobs <- lane
	}
	close(jobs)
	var opening sync.WaitGroup
	for range min(8, len(pending)) {
		opening.Go(func() {
			for lane := range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				x, err := lane.open(ctx, lane.network)
				cancel()
				if err != nil {
					failed <- err
					continue
				}
				if lane.client {
					cl[lane.index].Executor = x
				} else {
					sl[lane.index].Executor = x
				}
			}
		})
	}
	opening.Wait()
	close(failed)
	for err := range failed {
		t.Fatal("bounded listed-lane setup", err)
	}
	c, err := transport.NewClientAccountRoster(ch, plan, cl, f.wallet, paths, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s, err := transport.NewServerAccountRoster(sh, plan, sl, journals, f.key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		client bool
		r      *transport.RosterResult
		err    error
	}
	done := make(chan result, 2)
	go func() { r, err := c.Run(ctx); done <- result{true, r, err} }()
	go func() { r, err := s.Run(ctx); done <- result{false, r, err} }()
	pair := &accountRosterPair{f: f, journals: journals}
	var failure error
	for range 2 {
		r := <-done
		if r.err != nil {
			failure = errors.Join(failure, r.err)
			cancel()
		} else if r.client {
			pair.client = r.r
		} else {
			pair.server = r.r
		}
	}
	if failure != nil {
		t.Fatal("per-account accounted roster", failure, c.Snapshot(), s.Snapshot())
	}
	t.Cleanup(func() { pair.client.CloseWallets() })
	for _, j := range journals {
		m, ok := pair.client.Manifest().Account(j.Account)
		if !ok || m.ControlStore != j.Control.StoreID() || m.DataStore != j.Data.StoreID() {
			t.Fatal("original per-account issuer replaced", j.Account, m)
		}
		ws, ok := pair.client.Wallets(j.Account)
		if !ok || ws.Control.Identity().Store != m.ControlStore || ws.Data.Identity().Store != m.DataStore {
			t.Fatal("local wallet not bound to authenticated account", j.Account)
		}
		entries, err := ws.Data.Entries()
		if err != nil {
			t.Fatal(err)
		}
		if j.Account == "account2" {
			if len(entries) != 1 || !entries[0].Fenced || entries[0].Used.Bytes != 1024 || entries[0].Used.Messages != 1 {
				t.Fatal("old per-account wallet history reset", entries)
			}
		} else if len(entries) != 0 {
			t.Fatal("roster invented permission", j.Account, entries)
		}
	}
	return pair
}

func TestDistinctAccountRosterServiceCarriesTLSHTTPWithoutFlatteningTwentyOriginalIssuers(t *testing.T) {
	testAccountTLSHTTP(t, nil, nil)
}
func TestDistinctAccountRosterTLSHTTPWithFluctuatingClientAndServerService(t *testing.T) {
	testAccountTLSHTTP(t, []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}, []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 30 * time.Millisecond})
}
func testAccountTLSHTTP(t *testing.T, clientService, serverService []time.Duration) {
	testAccountTLSHTTPWithWarmSamples(t, clientService, serverService, 4)
}
func testAccountTLSHTTPWithWarmSamples(t *testing.T, clientService, serverService []time.Duration, warmSamples int) []qualification.Exchange {
	return testAccountTLSHTTPOnNetwork(t, clientService, serverService, warmSamples, nil, nil)
}
func testAccountTLSHTTPOnNetwork(t *testing.T, clientService, serverService []time.Duration, warmSamples int, clientNetwork, serverNetwork transport.NetworkDialer) []qualification.Exchange {
	p := distinctAccountRosterOnNetwork(t, 10, clientService, serverService, clientNetwork, serverNetwork)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(w, r.Body) }))
	defer upstream.Close()
	address := netip.MustParseAddrPort(upstream.Listener.Addr().String())
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	badKey := append([]byte(nil), p.f.key...)
	badKey[0] ^= 1
	if _, err := transport.NewRosterService(p.client, a, badKey, transport.RosterServiceOptions{ReceiveWindow: 256 << 10}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("foreign profile claimed roster service", err)
	}
	client, err := transport.NewRosterService(p.client, a, p.f.key, transport.RosterServiceOptions{ReceiveWindow: 256 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.NewRosterService(p.client, a, p.f.key, transport.RosterServiceOptions{ReceiveWindow: 256 << 10}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("completed roster assembled twice", err)
	}
	server, err := transport.NewRosterService(p.server, b, p.f.key, transport.RosterServiceOptions{ReceiveWindow: 256 << 10, ExitWorkers: 4, Dialer: &net.Dialer{}, Policy: transport.ExitPolicyFunc(func(_ context.Context, user string, target transport.Target) (netip.AddrPort, error) {
		if user != "12345678" || target.Host != address.Addr().String() || target.Port != address.Port() {
			return netip.AddrPort{}, transport.OpenDenied
		}
		return address, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	failed := make(chan error, 2)
	for _, svc := range []*transport.RosterService{client, server} {
		workers.Go(func() {
			if err := svc.Runtime.Run(ctx); err != nil && ctx.Err() == nil {
				failed <- err
				cancel()
			}
		})
	}
	defer func() { cancel(); workers.Wait(); client.Runtime.Close(); server.Runtime.Close() }()
	// Trusted explicit initial policy, not automatic replenishment on a timeout.
	for index, j := range p.journals {
		owner := server.Authorities[j.Account]
		if index != 0 {
			if _, err := owner.Issue(ctx, transport.ControlLedger, 1, pool.Amount{Bytes: 32 << 20, Messages: 32}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := owner.Issue(ctx, transport.DataLedger, 1, pool.Amount{Bytes: 32 << 20, Messages: 32}); err != nil {
			t.Fatal(err)
		}
	}
	// Application service starts after real current-session offers arrive. A
	// successful roster by itself is not the equivalent of a connected VPN.
	readyCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	for _, j := range p.journals {
		for _, class := range []transport.LedgerClass{transport.ControlLedger, transport.DataLedger} {
			for {
				ws, _ := p.client.Wallets(j.Account)
				w := ws.Data
				if class == transport.ControlLedger {
					w = ws.Control
				}
				entries, err := w.Entries()
				if err != nil {
					t.Fatal(err)
				}
				ready := false
				for _, e := range entries {
					if !e.Fenced && e.Offer.Key.Account == j.Account && e.Offer.Key.Session == w.Session() && e.Offer.Limit.Bytes > e.Used.Bytes && e.Offer.Limit.Messages > e.Used.Messages {
						ready = true
					}
				}
				if ready {
					break
				}
				select {
				case <-readyCtx.Done():
					t.Fatal("actual duplex permission did not arrive", j.Account, class, readyCtx.Err(), client.Runtime.Snapshot(), server.Runtime.Snapshot())
				case err := <-failed:
					t.Fatal(err)
				case <-time.After(time.Millisecond):
				}
			}
		}
	}
	certs := x509.NewCertPool()
	certs.AddCert(upstream.Certificate())
	tr := &http.Transport{DialContext: client.Dialer.DialContext, TLSClientConfig: &tls.Config{RootCAs: certs, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	fastReads := func() uint64 {
		var total uint64
		for _, svc := range []*transport.RosterService{client, server} {
			for _, c := range svc.Runtime.Snapshot().Carriers {
				if c.Plane == transport.Fast {
					total += c.IMAP.Fetch.Started + c.IMAP.Inventory.Started
				}
			}
		}
		return total
	}
	beforeReads := fastReads()
	applicationStarted := time.Now()
	for run, size := range []int{29, 256<<10 + 123, 71, 256<<10 + 317} {
		body := bytes.Repeat([]byte{byte(run + 1)}, size)
		started := time.Now()
		stopTrace, reportTrace := traceAccountRequest(t, ctx, p, a, b, client, server, uint64(2*run+1))
		response, err := hc.Post(upstream.URL, "application/octet-stream", bytes.NewReader(body))
		if err != nil {
			reportTrace()
			t.Fatal("actual multi-account TLS/HTTP", run, err, client.Runtime.Snapshot(), server.Runtime.Snapshot())
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		stopTrace()
		if err != nil || !bytes.Equal(body, got) {
			reportTrace()
			t.Fatal("application integrity", run, len(got), err)
		}
		t.Logf("per-account HTTP integrity run=%d payload=%d elapsed=%s", run, size, time.Since(started))
	}
	t.Logf("account application cohort elapsed=%s fast_read_commands=%d", time.Since(applicationStarted), fastReads()-beforeReads)
	// Warm HTTP exchanges isolate light-load application service from a cold
	// OPEN/TLS handshake. This is a diagnostic sample, not a release gate:
	// retain every value and report the unchanged 250ms target independently.
	warmTransport := tr.Clone()
	warmTransport.DisableKeepAlives = false
	defer warmTransport.CloseIdleConnections()
	warmClient := &http.Client{Transport: warmTransport, Timeout: 5 * time.Second}
	var samples []qualification.Exchange
	for sample := 0; sample <= warmSamples; sample++ {
		payload := []byte("warm small application exchange")
		started := time.Now()
		stopTrace, reportTrace := traceAccountRequest(t, ctx, p, a, b, client, server, 9)
		response, err := warmClient.Post(upstream.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			reportTrace()
			t.Fatal("warm account HTTP", sample, err)
		}
		got, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		elapsed := time.Since(started)
		stopTrace()
		if sample > 0 && elapsed > 250*time.Millisecond {
			reportTrace()
		}
		if readErr != nil || !bytes.Equal(got, payload) {
			reportTrace()
			t.Fatal("warm application integrity", sample, readErr)
		}
		if sample > 0 {
			samples = append(samples, qualification.Exchange{Elapsed: elapsed, Bytes: len(payload)})
			t.Logf("warm_light_sample=%d payload=%d elapsed_ns=%d target_ns=%d light_target_met=%t", sample, len(payload), elapsed.Nanoseconds(), (250 * time.Millisecond).Nanoseconds(), elapsed <= 250*time.Millisecond)
		}
	}
	for _, j := range p.journals {
		if err := j.Control.Audit(); err != nil {
			t.Fatal(j.Account, err)
		}
		if err := j.Data.Audit(); err != nil {
			t.Fatal(j.Account, err)
		}
		if client.Authorities[j.Account].Stats(transport.DataLedger).AcceptedOffers != 1 {
			t.Fatal("grant delivered to wrong account", j.Account)
		}
		if j.Account == "account2" {
			ws, _ := p.client.Wallets(j.Account)
			rows, err := ws.Data.Entries()
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range rows {
				if row.Offer.Key.Session == ws.Data.Session() {
					continue
				}
				found = true
				if !row.Fenced || row.Used.Bytes != 1024 || row.Used.Messages != 1 || row.Ack != (wire.Quota{}) || len(row.Pending) != 1 {
					t.Fatal("current authority overwrote old unknown history", row)
				}
				k := row.Offer.Key
				g, err := j.Data.GetGrant(journal.GrantRef{Store: k.Store, ID: k.Grant, Scope: journal.Scope{Account: k.Account, User: k.User, Session: k.Session}})
				if err != nil || g.Limit.Bytes != 8192 || g.Released != (pool.Amount{}) || g.Closed {
					t.Fatal("old unknown server reservation was silently refunded", g, err)
				}
			}
			if !found {
				t.Fatal("old wallet descriptor disappeared during current session")
			}
		}
	}
	return samples
}
