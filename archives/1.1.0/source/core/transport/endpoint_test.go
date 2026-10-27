package transport_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wire"
)

func TestPersistentEndpointOwnsStartupAndRenewsAccountedGrantsDuringTLSHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var stopping atomic.Bool
	defer stopping.Store(true)
	key := bytes.Repeat([]byte{17}, 32)
	template := transport.InvitationTemplate{Binding: secure.Binding{Server: wire.ID{1}, Pair: wire.ID{2}, User: "12345678", Epoch: 1, ConfigHash: [32]byte{5}}, Generation: 7}
	plan := transport.RosterPlan{ClientBudget: transport.RuntimeBudget{Connections: 4, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 4, PerAccount: 2}}
	client := transport.EndpointOptions{Client: true, Template: template, Key: key, Service: transport.RosterServiceOptions{ReceiveWindow: 256 << 10}}
	server := transport.EndpointOptions{Template: template, Key: key}
	root := t.TempDir()
	for n := 1; n <= 2; n++ {
		account := fmt.Sprintf("account%d", n)
		plan.Accounts = append(plan.Accounts, transport.RosterAccount{Account: account, FastFolder: "fast", BulkFolder: "bulk"})
		control := accountIssuer(t, filepath.Join(root, account+"-control.db"), account, []string{"fast", "bulk"})
		data := accountIssuer(t, filepath.Join(root, account+"-data.db"), account, []string{"fast", "bulk"})
		server.Journals = append(server.Journals, transport.RosterJournals{Account: account, Control: control, Data: data})
		paths := transport.RosterWalletPaths{Account: account, Data: filepath.Join(root, account+"-data-wallet.db")}
		if n == 1 {
			client.AnchorWallet = filepath.Join(root, account+"-control-wallet.db")
		} else {
			paths.Control = filepath.Join(root, account+"-control-wallet.db")
		}
		client.Wallets = append(client.Wallets, paths)
		for _, l := range []struct {
			plane  transport.Plane
			folder string
		}{{transport.Fast, "fast"}, {transport.Bulk, "bulk"}} {
			box := &sharedMailbox{keywords: true, reclamation: true, allowClose: stopping.Load}
			cx := box.selectedAccountService(t, account, l.folder, nil, true, false)
			sx := box.selectedAccountService(t, account, l.folder, nil, true, false)
			t.Cleanup(func() { cx.Close(); sx.Close() })
			if err := transport.InventoryRetained(ctx, sx, "12345678", key); err != nil {
				t.Fatal(err)
			}
			client.Lanes = append(client.Lanes, transport.RosterLane{Plane: l.plane, Executor: cx})
			server.Lanes = append(server.Lanes, transport.RosterLane{Plane: l.plane, Executor: sx})
		}
	}
	client.Plan, server.Plan = plan, plan
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		if e != nil {
			t.Log("target request read", len(body), e)
			http.Error(w, "request read failed", 500)
			return
		}
		w.Write(body)
	}))
	defer upstream.Close()
	address := netip.MustParseAddrPort(upstream.Listener.Addr().String())
	server.Service = transport.RosterServiceOptions{ReceiveWindow: 256 << 10, ExitWorkers: 4, Dialer: &net.Dialer{}, Policy: transport.ExitPolicyFunc(func(_ context.Context, user string, target transport.Target) (netip.AddrPort, error) {
		if user != "12345678" || target.Host != address.Addr().String() || target.Port != address.Port() {
			return netip.AddrPort{}, transport.OpenDenied
		}
		return address, nil
	})}
	type started struct {
		client bool
		s      *transport.EndpointSession
		err    error
	}
	done := make(chan started, 2)
	go func() { s, err := transport.StartEndpoint(ctx, server); done <- started{false, s, err} }()
	go func() { s, err := transport.StartEndpoint(ctx, client); done <- started{true, s, err} }()
	var c, s *transport.EndpointSession
	for range 2 {
		r := <-done
		if r.err != nil {
			cancel()
			t.Fatal("production endpoint startup", r.client, r.err)
		}
		if r.client {
			c = r.s
		} else {
			s = r.s
		}
	}
	defer func() {
		// User shutdown can interrupt an uncommitted control APPEND. Only this
		// explicit endpoint close permits EOF; all 80 actual transfers stay strict.
		stopping.Store(true)
		s.Close()
		c.Close()
	}()
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	tr := &http.Transport{DialContext: c.Service.Dialer.DialContext, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 1500 * time.Millisecond, ResponseHeaderTimeout: 1500 * time.Millisecond}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	// More than the original 32-message lifetime permits: continuation must
	// come from real overlapping offers, fences, storage proofs and retirement.
	for run := 0; run < 80; run++ {
		payload := bytes.Repeat([]byte{byte(run)}, 70<<10)
		resp, err := hc.Post(upstream.URL, "application/octet-stream", bytes.NewReader(payload))
		if err != nil {
			t.Fatal("persistent TLS/HTTP", run, err, c.Err(), s.Err(), c.Service.Runtime.Snapshot(), s.Service.Runtime.Snapshot())
		}
		got, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || !bytes.Equal(got, payload) {
			t.Fatal("application integrity", run, len(got), resp.StatusCode, err, c.Err(), s.Err(), c.Service.Runtime.Snapshot(), s.Service.Runtime.Snapshot())
		}
	}
	var accepted uint64
	for _, a := range c.Service.Authorities {
		accepted += a.Stats(transport.DataLedger).AcceptedOffers
	}
	if accepted <= 2 {
		t.Fatal("no actual DATA grant renewal", accepted)
	}
	for _, j := range server.Journals {
		if err := j.Control.Audit(); err != nil {
			t.Fatal(err)
		}
		if err := j.Data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("accounted renewed DATA offers", accepted)
}
