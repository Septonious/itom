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
	"itom.local/core/wire"
)

// Application-integrity fixture, NOT provider/rate qualification: Noise and
// grants are pre-established. Independent read/publication workers use four
// fixed scoped executors with a fixture readiness hint. Real target TCP,
// TLS/HTTP, authenticated OPEN, quota persistence, TLS/IMAP and UID discovery do
// execute. No streams are manually added and own mailbox writes reach both ends.
func TestColdTLSHTTPAndWindowCrossingBodiesThroughAccountedIMAP(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(w, r.Body) }))
	defer upstream.Close()
	address := netip.MustParseAddrPort(upstream.Listener.Addr().String())
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	ce, se := sessionEpochs(t, wire.ID{95})
	ca, err := transport.NewCipher(ce)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := transport.NewCipher(se)
	if err != nil {
		t.Fatal(err)
	}
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
	key := bytes.Repeat([]byte{17}, 32)
	scope := journal.Scope{Account: "account1", User: "12345678", Session: wire.ID{95}}
	stores := map[transport.LedgerClass]*journal.Store{}
	wallets := map[transport.LedgerClass]*wallet.Wallet{}
	for _, class := range []transport.LedgerClass{transport.DataLedger, transport.ControlLedger} {
		store := issuer(t, filepath.Join(t.TempDir(), "issuer.db"), 64<<20, 128)
		stores[class] = store
		w, err := wallet.OpenBound(filepath.Join(t.TempDir(), "wallet.db"), store.StoreID(), ce)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		wallets[class] = w
		grant, err := store.IssueRemote(scope, pool.Amount{Bytes: 32 << 20, Messages: 32})
		if err != nil {
			t.Fatal(err)
		}
		offer, err := store.RemoteOffer(grant.Ref)
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Accept(offer); err != nil {
			t.Fatal(err)
		}
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
		inboxes        []*transport.Inbox
		routes         map[transport.LedgerClass]route
		writers        []*transport.PublicationWorker
	}
	var services []*service
	for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
		folder := "fast"
		if plane == transport.Bulk {
			folder = "bulk"
		}
		box := &sharedMailbox{changed: make(chan struct{}, 1)}
		s := &service{plane: plane, box: box, client: box.selected(t, folder), server: box.selected(t, folder), routes: map[transport.LedgerClass]route{}}
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
				inbox, err = transport.NewClientInbox(end.x, transport.DefaultInboxLimits(), receivers...)
			} else {
				var receivers []*transport.RemoteReceiver
				for _, r := range s.routes {
					receivers = append(receivers, r.up)
				}
				inbox, err = transport.NewServerInbox(end.x, transport.DefaultInboxLimits(), receivers...)
			}
			if err != nil {
				t.Fatal(err)
			}
			s.inboxes = append(s.inboxes, inbox)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	failed := make(chan error, 1)
	workers.Go(func() { exit.Run(ctx) })
	for _, s := range services {
		for _, writer := range s.writers {
			workers.Go(func() {
				if err := writer.Run(ctx); err != nil && ctx.Err() == nil {
					select {
					case failed <- err:
					default:
					}
					cancel()
				}
			})
		}
	}
	for _, s := range services {
		for index, i := range s.inboxes {
			owner := a
			if index == 1 {
				owner = b
			}
			workers.Go(func() {
				fail := func(err error) {
					if ctx.Err() == nil {
						select {
						case failed <- err:
						default:
						}
						cancel()
					}
				}
				for ctx.Err() == nil {
					work, hint := owner.WorkChanged(), s.box.Changes()
					var p transport.InboxProgress
					var err error
					if s.plane == transport.Fast {
						p, err = i.Poll(ctx)
					} else {
						p, err = i.Sweep(ctx)
					}
					if err != nil {
						fail(err)
						return
					}
					if p.Applied == 0 && !p.More {
						select {
						case <-ctx.Done():
							return
						case <-work:
						case <-hint:
						}
					}
				}
			})
		}
	}
	// Stop application/exit work before closing the four fixed carrier sockets.
	t.Cleanup(func() { cancel(); workers.Wait(); a.Close(); b.Close() })
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
			for _, sample := range trace {
				t.Log(sample)
			}
			for class, store := range stores {
				t.Logf("class=%d commits=%+v", class, store.CommitSnapshot())
			}
			t.Logf("client state=%+v server state=%+v", a.Snapshot(), b.Snapshot())
			for _, s := range services {
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
	cancel()
	workers.Wait()
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
