package transport_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
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
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// Application-integrity fixture, NOT provider/rate qualification: Noise and
// grants are pre-established and one pump owns both endpoints. Real target TCP,
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
			// Client classification remains fixture-owned. The server below uses
			// the production scoped decoder and original persisted attempt index.
			var decode transport.InboxDecoder = func(validity uint32, physical transport.Body) (transport.InboxDelivery, error) {
				charge, err := mailbox.CanonicalEncodedBytes(physical.Bytes)
				if err != nil {
					return nil, err
				}
				body, err := mailbox.Decode(physical.Bytes)
				if err != nil {
					return nil, err
				}
				serverOrigin := len(body) >= 4 && (string(body[:4]) == "ISD2" || string(body[:4]) == "ISC2")
				var usage wire.UsageReport
				if serverOrigin {
					_, usage, _, err = wire.OpenServerEnvelope(body, key, charge)
				} else {
					usage, _, err = wire.OpenDataEnvelope(body, key, charge)
				}
				if err != nil {
					return nil, err
				}
				var r route
				for _, candidate := range s.routes {
					if candidate.store.StoreID() == usage.Key.Store {
						r = candidate
					}
				}
				if r.store == nil || usage.Attempt == nil || usage.Key.Account != scope.Account || usage.Key.User != scope.User || usage.Key.Session != scope.Session || usage.Attempt.Folder != folder {
					return nil, transport.ErrProtocol
				}
				if serverOrigin {
					return r.down.Decode(validity, physical.UID, physical.Bytes)
				}
				return nil, nil // own BODY is neither peer application data nor GC proof
			}
			if !end.client {
				var receivers []*transport.RemoteReceiver
				for _, r := range s.routes {
					receivers = append(receivers, r.up)
				}
				decode, err = transport.NewServerInboxDecoder(receivers...)
				if err != nil {
					t.Fatal(err)
				}
			}
			i, err := transport.NewInbox(end.x, transport.InboxScope{Account: "account1", Folder: folder, UIDValidity: 9}, transport.DefaultInboxLimits(), decode)
			if err != nil {
				t.Fatal(err)
			}
			s.inboxes = append(s.inboxes, i)
		}
		services = append(services, s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	failed := make(chan error, 1)
	workers.Go(func() { exit.Run(ctx) })
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
			left, right := a.WorkChanged(), b.WorkChanged()
			progress := false
			for _, s := range services {
				for _, class := range []transport.LedgerClass{transport.ControlLedger, transport.DataLedger} {
					r, ok := s.routes[class]
					if !ok {
						continue
					}
					for _, client := range []bool{true, false} {
						var p *transport.PreparedPublication
						var err error
						if client {
							if s.plane == transport.Fast {
								p, err = r.client.Prepare(ctx, transport.MaxPayload)
							} else {
								p, err = r.client.PrepareBulk(ctx, transport.MaxBulkBytes)
							}
						} else {
							if s.plane == transport.Fast {
								p, err = r.server.Prepare(ctx, transport.MaxPayload)
							} else {
								p, err = r.server.PrepareBulk(ctx, transport.MaxBulkBytes)
							}
						}
						if err != nil {
							fail(err)
							return
						}
						if p == nil {
							continue
						}
						x := s.server
						if client {
							x = s.client
						}
						if outcome, err := p.Start(ctx, x); err != nil || outcome != transport.Accepted {
							if err == nil {
								err = transport.ErrUnknown
							}
							fail(err)
							return
						}
						progress = true
					}
				}
				for _, i := range s.inboxes {
					p, err := i.Sweep(ctx)
					if err != nil {
						fail(err)
						return
					}
					progress = progress || p.Applied > 0
				}
			}
			if !progress {
				select {
				case <-ctx.Done():
					return
				case <-left:
				case <-right:
				case <-services[0].box.changed:
				case <-services[1].box.changed:
				}
			}
		}
	})
	// Stop application/exit work before closing the four fixed carrier sockets.
	t.Cleanup(func() { cancel(); workers.Wait(); a.Close(); b.Close() })
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	h := &http.Transport{DialContext: dialer.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond, DisableKeepAlives: true}
	defer h.CloseIdleConnections()
	client := &http.Client{Transport: h, Timeout: 5 * time.Second}
	for run, size := range []int{29, (256 << 10) + 123, 71, (256 << 10) + 317} {
		payload := bytes.Repeat([]byte{byte(run + 1)}, size)
		response, err := client.Post(upstream.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
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
