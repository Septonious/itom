package transport_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/secure"
	"itom.local/core/transport"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

func TestEndpointRestartsPreserveWalletSpendAndClearedWalletCannotReleaseUnknownGrants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	key := bytes.Repeat([]byte{17}, 32)
	control := accountIssuer(t, filepath.Join(root, "control.db"), "account1", []string{"fast", "bulk"})
	data := accountIssuer(t, filepath.Join(root, "data.db"), "account1", []string{"fast", "bulk"})
	boxes := []*sharedMailbox{{keywords: true, reclamation: true}, {keywords: true, reclamation: true}}
	template := transport.InvitationTemplate{Binding: secure.Binding{Server: wire.ID{1}, Pair: wire.ID{2}, User: "12345678", Epoch: 1, ConfigHash: [32]byte{5}}, Generation: 7}
	plan := transport.RosterPlan{Accounts: []transport.RosterAccount{{Account: "account1", FastFolder: "fast", BulkFolder: "bulk"}}, ClientBudget: transport.RuntimeBudget{Connections: 2, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 2, PerAccount: 2}}
	var previous []wallet.Entry
	for round := 0; round < 3; round++ {
		paths := filepath.Join(root, "original")
		if round == 2 {
			paths = filepath.Join(root, "reinstalled")
		}
		// Standalone test files share an existing private directory, as on a
		// persistent endpoint. Reinstall has distinct files and no old history.
		anchor := paths + "-control-wallet.db"
		dataPath := paths + "-wallet.db"
		c := transport.EndpointOptions{Client: true, Template: template, Plan: plan, Key: key, AnchorWallet: anchor, Wallets: []transport.RosterWalletPaths{{Account: "account1", Data: dataPath}}, Service: transport.RosterServiceOptions{ReceiveWindow: 256 << 10}}
		s := transport.EndpointOptions{Template: template, Plan: plan, Key: key, Journals: []transport.RosterJournals{{Account: "account1", Control: control, Data: data}}, Service: transport.RosterServiceOptions{ReceiveWindow: 256 << 10, ExitWorkers: 1, Dialer: &net.Dialer{}, Policy: transport.ExitPolicyFunc(func(context.Context, string, transport.Target) (netip.AddrPort, error) {
			return netip.AddrPort{}, transport.OpenDenied
		})}}
		for n, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
			folder := "fast"
			if plane == transport.Bulk {
				folder = "bulk"
			}
			cx := boxes[n].selectedAccountService(t, "account1", folder, nil, true, false)
			sx := boxes[n].selectedAccountService(t, "account1", folder, nil, true, false)
			c.Lanes = append(c.Lanes, transport.RosterLane{Plane: plane, Executor: cx})
			s.Lanes = append(s.Lanes, transport.RosterLane{Plane: plane, Executor: sx})
		}
		type result struct {
			client bool
			s      *transport.EndpointSession
			e      error
		}
		done := make(chan result, 2)
		go func() { x, e := transport.StartEndpoint(ctx, s); done <- result{false, x, e} }()
		go func() { x, e := transport.StartEndpoint(ctx, c); done <- result{true, x, e} }()
		var client, server *transport.EndpointSession
		for range 2 {
			r := <-done
			if r.e != nil {
				cancel()
				t.Fatal("restart", round, r.client, r.e)
			}
			if r.client {
				client = r.s
			} else {
				server = r.s
			}
		}
		ws, _ := client.Roster.Wallets("account1")
		if round > 0 {
			wait, stop := context.WithTimeout(ctx, 3*time.Second)
			for {
				all := true
				for _, old := range previous {
					k := old.Offer.Key
					g, e := data.GetGrant(journal.GrantRef{Store: k.Store, ID: k.Grant, Scope: journal.Scope{Account: k.Account, User: k.User, Session: k.Session}})
					if e != nil {
						t.Fatal(e)
					}
					if round == 1 && !g.Closed {
						all = false
					}
					if round == 2 && (g.Closed || g.Released.Bytes != 0 || g.Released.Messages != 0) {
						t.Fatal("cleared history invented an unused refund", g)
					}
				}
				if all {
					break
				}
				select {
				case <-wait.Done():
					t.Fatal("surviving original wallet did not fence its actual prior spend", wait.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			stop()
		}
		rows, e := ws.Data.Entries()
		if e != nil {
			t.Fatal(e)
		}
		previous = nil
		for _, row := range rows {
			if row.Offer.Key.Session == ws.Data.Session() {
				previous = append(previous, row)
			}
		}
		if len(previous) == 0 {
			t.Fatal("no actual current-session DATA offer")
		}
		if e = client.Close(); e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		if e = server.Close(); e != nil && !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		if e = control.Audit(); e != nil {
			t.Fatal(e)
		}
		if e = data.Audit(); e != nil {
			t.Fatal(e)
		}
	}
}
