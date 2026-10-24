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
	"path/filepath"
	"sync"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
)

// Application-integrity fixture, NOT provider/rate qualification: initial control
// permission and later DATA offers come from the authority fixture. Noise keys
// are established/confirmed over accounted IMAP. Independent workers use four
// fixed scoped executors without a fixture readiness hint. Real target TCP,
// TLS/HTTP, authenticated OPEN, quota persistence, TLS/IMAP and UID discovery do
// execute. No streams are manually added and own mailbox writes reach both ends.
func TestColdTLSHTTPAndWindowCrossingBodiesThroughAccountedIMAP(t *testing.T) {
	runColdAccountedHTTP(t, nil, nil, false)
}

func TestColdTLSHTTPWithFluctuatingClientAndServerIMAPCommandService(t *testing.T) {
	// The user's latency ranges motivate variable service, but these are
	// command-delay integrity checks, not equivalent raw path RTT measurements.
	runColdAccountedHTTP(t,
		[]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 18 * time.Millisecond, 42 * time.Millisecond, 27 * time.Millisecond},
		[]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 24 * time.Millisecond, 36 * time.Millisecond}, false)
}

func TestColdTLSHTTPWithQuietBulkIdleAndFluctuatingIMAPService(t *testing.T) {
	runColdAccountedHTTP(t,
		[]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 18 * time.Millisecond, 42 * time.Millisecond, 27 * time.Millisecond},
		[]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 24 * time.Millisecond, 36 * time.Millisecond}, true)
}

func runColdAccountedHTTP(t *testing.T, clientService, serverService []time.Duration, idle bool) {
	t.Helper()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(w, r.Body) }))
	defer upstream.Close()
	address := netip.MustParseAddrPort(upstream.Listener.Addr().String())
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	bootstrap := bootstrapFixtureWithService(t, true, clientService, serverService, idle)
	exchangeBootstrap(t, bootstrap.client, bootstrap.server, bootstrap.cx, bootstrap.sx)
	ca, err := bootstrap.client.Cipher()
	if err != nil {
		t.Fatal(err)
	}
	cb, err := bootstrap.server.Cipher()
	if err != nil {
		t.Fatal(err)
	}
	ce, err := bootstrap.client.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	clientHandoff, err := bootstrap.client.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	serverHandoff, err := bootstrap.server.Handoff()
	if err != nil {
		t.Fatal(err)
	}
	bootstrap.client.Close()
	bootstrap.server.Close()
	dialer, err := transport.NewClientDialer(a, 256<<10)
	if err != nil {
		t.Fatal(err)
	}
	exit, err := transport.NewExitServer(b, cb, 256<<10, 4, transport.ExitPolicyFunc(func(_ context.Context, user string, target transport.Target) (netip.AddrPort, error) {
		if user != "12345678" || target.Host != address.Addr().String() || target.Port != address.Port() {
			return netip.AddrPort{}, transport.OpenDenied
		}
		return address, nil
	}), &net.Dialer{})
	if err != nil {
		t.Fatal(err)
	}
	key := bootstrap.key
	scope := journal.Scope{Account: "account1", User: bootstrap.binding.User, Session: bootstrap.binding.Session}
	stores := map[transport.LedgerClass]*journal.Store{transport.ControlLedger: bootstrap.store}
	wallets := map[transport.LedgerClass]*wallet.Wallet{transport.ControlLedger: bootstrap.wallet}
	dataStore := issuer(t, filepath.Join(t.TempDir(), "data.db"), 64<<20, 128)
	stores[transport.DataLedger] = dataStore
	dataWallet, err := wallet.OpenBound(filepath.Join(t.TempDir(), "data-wallet.db"), dataStore.StoreID(), ce)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dataWallet.Close() })
	wallets[transport.DataLedger] = dataWallet
	// DATA grant delivery is still supplied by this authority fixture, not a
	// claim that the production grant/bootstrap protocol has been completed.
	grant, err := dataStore.IssueRemote(scope, pool.Amount{Bytes: 32 << 20, Messages: 32})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := dataStore.RemoteOffer(grant.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = dataWallet.Accept(offer); err != nil {
		t.Fatal(err)
	}
	type route struct {
		client *transport.ClientPublisher
		server *transport.ServerPublisher
		up     *transport.RemoteReceiver
		down   *transport.ClientReceiver
		store  *journal.Store
	}
	type service struct {
		plane          transport.Plane
		box            *sharedMailbox
		client, server *transport.Executor
		routes         map[transport.LedgerClass]route
		writers        []*transport.PublicationWorker
		readers        []*transport.InboxWorker
	}
	var services []*service
	for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
		folder := "fast"
		if plane == transport.Bulk {
			folder = "bulk"
		}
		box := &sharedMailbox{}
		var clientExecutor, serverExecutor *transport.Executor
		if plane == transport.Fast {
			box, clientExecutor, serverExecutor = bootstrap.box, bootstrap.cx, bootstrap.sx
		} else {
			clientExecutor, serverExecutor = box.selectedWithIdleService(t, folder, clientService, idle, false), box.selectedWithIdleService(t, folder, serverService, idle, false)
		}
		s := &service{plane: plane, box: box, client: clientExecutor, server: serverExecutor, routes: map[transport.LedgerClass]route{}}
		t.Cleanup(func() { s.client.Close(); s.server.Close() })
		for _, class := range []transport.LedgerClass{transport.ControlLedger, transport.DataLedger} {
			if plane == transport.Bulk && class == transport.ControlLedger {
				continue
			}
			r := route{store: stores[class]}
			r.client, err = transport.NewClientPublisher(a, plane, class, ca, wallets[class], "account1", folder, key)
			if err != nil {
				t.Fatal(err)
			}
			r.server, err = transport.NewServerPublisher(b, plane, class, cb, r.store, scope, folder, key)
			if err != nil {
				t.Fatal(err)
			}
			r.up, err = transport.NewRemoteReceiver(b, class, cb, r.store, "account1", "12345678", folder, 9, key)
			if err != nil {
				t.Fatal(err)
			}
			r.down, err = transport.NewClientReceiver(a, class, ca, transport.ServerOrigin{Store: r.store.StoreID(), Run: r.store.Run(), Account: "account1", User: "12345678", Folder: folder, UIDValidity: 9}, key)
			if err != nil {
				t.Fatal(err)
			}
			s.routes[class] = r
		}
		for _, end := range []struct {
			x      *transport.Executor
			client bool
		}{{s.client, true}, {s.server, false}} {
			var inbox *transport.Inbox
			if end.client {
				var receivers []*transport.ClientReceiver
				for _, r := range s.routes {
					receivers = append(receivers, r.down)
				}
				if plane == transport.Fast {
					inbox, err = transport.NewClientInboxWithBootstrap(end.x, transport.DefaultInboxLimits(), clientHandoff, receivers...)
				} else {
					inbox, err = transport.NewClientInbox(end.x, transport.DefaultInboxLimits(), receivers...)
				}
			} else {
				var receivers []*transport.RemoteReceiver
				for _, r := range s.routes {
					receivers = append(receivers, r.up)
				}
				if plane == transport.Fast {
					inbox, err = transport.NewServerInboxWithBootstrap(end.x, transport.DefaultInboxLimits(), serverHandoff, receivers...)
				} else {
					inbox, err = transport.NewServerInbox(end.x, transport.DefaultInboxLimits(), receivers...)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			readLimits := transport.DefaultInboxServiceLimits()
			readLimits.PreferIdle = plane == transport.Bulk
			reader, err := transport.NewInboxWorker(inbox, readLimits)
			if err != nil {
				t.Fatal(err)
			}
			s.readers = append(s.readers, reader)
		}
		var clientPublishers []*transport.ClientPublisher
		var serverPublishers []*transport.ServerPublisher
		for _, r := range s.routes {
			clientPublishers = append(clientPublishers, r.client)
			serverPublishers = append(serverPublishers, r.server)
		}
		clientWriter, err := transport.NewClientPublicationWorker(s.client, plane, clientPublishers...)
		if err != nil {
			t.Fatal(err)
		}
		serverWriter, err := transport.NewServerPublicationWorker(s.server, plane, serverPublishers...)
		if err != nil {
			t.Fatal(err)
		}
		s.writers = []*transport.PublicationWorker{clientWriter, serverWriter}
		services = append(services, s)
	}
	var clientCarriers, serverCarriers []transport.RuntimeCarrier
	for _, s := range services {
		clientCarriers = append(clientCarriers, transport.RuntimeCarrier{Reader: s.readers[0], Writer: s.writers[0]})
		serverCarriers = append(serverCarriers, transport.RuntimeCarrier{Reader: s.readers[1], Writer: s.writers[1]})
	}
	for name, candidates := range map[string][]transport.RuntimeCarrier{
		"duplicate_socket": {clientCarriers[0], clientCarriers[0], clientCarriers[1]},
		"mixed_halves":     {{Reader: clientCarriers[0].Reader, Writer: clientCarriers[1].Writer}, clientCarriers[1]},
	} {
		if _, err := transport.NewRuntime(a, nil, transport.RuntimeBudget{Connections: 3, PerAccount: 3}, candidates...); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal(name, "invalid carrier ownership accepted", err)
		}
	}
	if _, err := transport.NewRuntime(a, nil, transport.RuntimeBudget{Connections: 1, PerAccount: 2}, clientCarriers...); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("runtime exceeded the explicit socket budget", err)
	}
	clientRuntime, err := transport.NewRuntime(a, nil, transport.RuntimeBudget{Connections: 2, PerAccount: 2}, clientCarriers...)
	if err != nil {
		t.Fatal(err)
	}
	serverRuntime, err := transport.NewRuntime(b, exit, transport.RuntimeBudget{Connections: 3, PerAccount: 3}, serverCarriers...)
	if err != nil {
		clientRuntime.Close()
		t.Fatal(err)
	}
	if _, err := transport.NewRuntime(a, nil, transport.RuntimeBudget{Connections: 2, PerAccount: 2}, clientCarriers...); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("second runtime claimed the same logical owner", err)
	}
	stopped, stop := context.WithCancel(context.Background())
	stop()
	if err := clientCarriers[0].Reader.Run(stopped); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("external read run stole runtime ownership", err)
	}
	if err := clientCarriers[0].Writer.Run(stopped); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("external write run stole runtime ownership", err)
	}
	if err := exit.Run(stopped); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("external exit run stole runtime ownership", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	failed := make(chan error, 2)
	for _, runtime := range []*transport.Runtime{clientRuntime, serverRuntime} {
		workers.Go(func() {
			if err := runtime.Run(ctx); err != nil && ctx.Err() == nil {
				select {
				case failed <- err:
				default:
				}
				cancel()
			}
		})
	}
	// The runtime owns final session/carrier shutdown; original stores and
	// wallets remain borrowed and available for the post-run debt audit.
	t.Cleanup(func() { cancel(); clientRuntime.Close(); serverRuntime.Close(); workers.Wait() })
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	h := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond, DisableKeepAlives: true}
	defer h.CloseIdleConnections()
	client := &http.Client{Transport: h, Timeout: 5 * time.Second}
	for run, size := range []int{29, (256 << 10) + 123, 71, (256 << 10) + 317} {
		payload := bytes.Repeat([]byte{byte(run + 1)}, size)
		var traceMu sync.Mutex
		var trace []string
		traceCtx, stopTrace := context.WithCancel(ctx)
		var tracing sync.WaitGroup
		start := time.Now()
		tracing.Go(func() {
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-traceCtx.Done():
					return
				case <-ticker.C:
					left, _ := a.FlowSnapshot(uint64(2*run + 1))
					right, _ := b.FlowSnapshot(uint64(2*run + 1))
					traceMu.Lock()
					if len(trace) < 50 {
						trace = append(trace, fmt.Sprintf("%s client=%+v server=%+v", time.Since(start), left, right))
					}
					traceMu.Unlock()
				}
			}
		})
		response, err := client.Post(upstream.URL, "application/octet-stream", bytes.NewReader(payload))
		stopTrace()
		tracing.Wait()
		if err != nil {
			t.Logf("client runtime=%+v server runtime=%+v", clientRuntime.Snapshot(), serverRuntime.Snapshot())
			for _, sample := range trace {
				t.Log(sample)
			}
			for class, store := range stores {
				t.Logf("class=%d commits=%+v", class, store.CommitSnapshot())
			}
			t.Logf("client state=%+v server state=%+v", a.Snapshot(), b.Snapshot())
			for _, s := range services {
				for side, r := range s.readers {
					t.Logf("plane=%d side=%d reader=%+v", s.plane, side, r.Stats())
				}
				for side, w := range s.writers {
					t.Logf("plane=%d side=%d writer=%+v", s.plane, side, w.Stats())
				}
			}
			select {
			case cause := <-failed:
				t.Fatal("IMAP pump", cause)
			default:
			}
			t.Fatal("cold OPEN/TLS/HTTP", run, err)
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatal("application echo changed or stalled", run, err)
		}
	}
	cancel()
	workers.Wait()
	for _, runtime := range []*transport.Runtime{clientRuntime, serverRuntime} {
		if status := runtime.Snapshot(); status.Phase != transport.ServiceStopped || status.Session.Closed {
			t.Fatal("normal cancellation destroyed logical owner or misreported a failure", status)
		}
	}
	for _, s := range services {
		for _, x := range []*transport.Executor{s.client, s.server} {
			cmd, err := x.Noop(context.Background())
			if err != nil {
				t.Fatal("healthy carrier lost", err)
			}
			if _, err = cmd.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
	select {
	case err := <-failed:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	default:
	}
	for class, store := range stores {
		if err := store.Audit(); err != nil {
			t.Fatal(err)
		}
		entries, err := wallets[class].Entries()
		if err != nil || len(entries) != 1 || entries[0].Used.Messages == 0 || len(entries[0].Pending) == 0 {
			t.Fatal("application success released physical debt", class, err)
		}
	}
}
