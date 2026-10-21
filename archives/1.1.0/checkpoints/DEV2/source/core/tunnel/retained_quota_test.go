package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// Ten real persistent ledgers: five legacy 8 MiB journals have retained remote
// byte/ordinal debt matching the field's 10/20 bootstrap failure. Restart them,
// publish both classes, exercise durable DATA spending on the resulting grants,
// and verify that none of the old uncertainty was forgiven to obtain readiness.
func TestRetainedQuotaTenAccountsReachTwentyDirections(t *testing.T) {
	ctx := context.Background()
	ready := 0
	for i := 0; i < 10; i++ {
		t.Run(fmt.Sprintf("account_%02d", i+1), func(t *testing.T) {
			a := servercfg.Account{ID: fmt.Sprintf("a%02d", i+1), Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
			dir := t.TempDir()
			s, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile8), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			var old journal.Grant
			if i < 5 {
				old, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 5 << 20, Messages: 80})
				if err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.Held(a.ID, u.ID)
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = ledger(dir, "data", a, u, false)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			l := &lane{ctx: ctx, side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 64), controlQueue: make(chan queuedControl, 64), maintenanceQueue: make(chan queuedControl, 64)}
			establishRecoveredServer(t, l)
			if err = l.offer(); err != nil {
				t.Fatal(err)
			}
			fast, bulk := l.issuedGrantCounts()
			if !l.ready.Load() || fast != 1 || bulk < 1 {
				t.Fatalf("stuck at grants: ready=%t fast=%d bulk=%d", l.ready.Load(), fast, bulk)
			}
			w, err := wallet.OpenBound(filepath.Join(dir, "fresh-wallet.db"), s.StoreID(), l.cipher)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			var bulkGrant wire.StorageGrant
			for len(l.urgentQueue) > 0 {
				frame := (<-l.urgentQueue).Frame
				c, err := wire.DecodeControl(frame)
				if err != nil {
					t.Fatal(err)
				}
				grant, ok := c.(wire.StorageGrant)
				if !ok {
					continue
				}
				if err = w.Accept(grant); err != nil {
					t.Fatal(err)
				}
				if grant.Limit.Messages != fastLeaseMessages || grant.Limit.Bytes != fastLeaseBytes {
					bulkGrant = grant
				}
			}
			if bulkGrant.Key.Grant == 0 {
				t.Fatal("no published bulk grant")
			}
			// A reduced lease must fit the largest permitted physical BODY, not strand
			// an already sealed batch in an endless WALLET_EXHAUSTED retry loop.
			if bulkGrant.Limit.Bytes < dataBatchMIMEHardCap {
				t.Fatal("bulk lease smaller than a legal batch")
			}
			report, body, err := w.Spend(bulkGrant.Key, u.Up, bytes.Repeat([]byte{1}, 32768), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
			if err != nil {
				t.Fatal("new bulk DATA not spendable", err)
			}
			if len(body) == 0 {
				t.Fatal("empty DATA")
			}
			if _, err = s.ApplyRemote(u.ID, s.Run(), report); err != nil {
				t.Fatal(err)
			}
			// Outbound bulk and latency must both remain usable alongside new inbound.
			local, err := l.tryLocalGrant(dataBatchMIMEHardCap, false)
			if err != nil {
				t.Fatal("outbound bulk stranded", err)
			}
			if err = s.CloseGrant(local.Ref); err != nil {
				t.Fatal(err)
			}
			latency, err := l.tryLocalGrant(dataBatchMIMEHardCap, true)
			if err != nil {
				t.Fatal("local latency stranded", err)
			}
			if err = s.CloseGrant(latency.Ref); err != nil {
				t.Fatal(err)
			}
			if i < 5 {
				unchanged, err := s.GetGrant(old.Ref)
				if err != nil || unchanged != old {
					t.Fatal("historical debt changed without proof", unchanged, err)
				}
			}
			after, err := s.Held(a.ID, u.ID)
			if err != nil || after.Bytes < before.Bytes || after.Messages < before.Messages || after.Bytes > dataProfile8.Bytes || after.Messages > dataProfile8.Messages {
				t.Fatal("invalid durable exposure", before, after, err)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
			ready += 2
		})
	}
	if ready != 20 {
		t.Fatalf("only %d/20 directions", ready)
	}
}

// Exercise the complete Noise/gate/coordinator/grant and striped TCP paths over
// actual TLS IMAP sockets. Half of the accounts restart with old reservations;
// a fresh client wallet cannot prove those reservations unused.
func TestRetainedQuotaTLSIMAPTenAccounts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := servercfg.New("retained-quota")
	if err != nil {
		t.Fatal(err)
	}
	u, err := cfg.AddUser("retained")
	if err != nil {
		t.Fatal(err)
	}
	logical, generation, err := freshAttachment()
	if err != nil {
		t.Fatal(err)
	}
	clientStripe := newStripeGroup(logical, generation)
	hub := newStripeHub()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	done := make(chan error, 20)
	servers := make(chan *lane, 10)
	clients := make([]*lane, 0, 10)
	for i := 0; i < 10; i++ {
		a := servercfg.Account{ID: fmt.Sprintf("a%02d", i+1), Mail: mailbox.Config{Host: "mail.example", Port: 993, TLS: "tls", Username: "test", Password: "test"}}
		dir := t.TempDir()
		s, e := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile8), time.Second)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.EnableAfterInventory(a.ID, u.ID, s.Run()); e != nil {
			t.Fatal(e)
		}
		if i < 5 {
			if _, e = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 5 << 20, Messages: 80}); e != nil {
				t.Fatal(e)
			}
		}
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
		cm, sm := tlsMailPair(t, ctx, []string{u.Up, u.Down})
		go func(a servercfg.Account, dir string, sm mailbox.Transport) {
			l, e := newLane(ctx, cfg.Server, u, a, dir, true, sm)
			if e == nil {
				l.allowPrivate = true
				e = hub.attach(l)
			}
			if e == nil {
				servers <- l
				e = l.run()
			} else if l != nil {
				l.close()
			}
			done <- e
		}(a, dir, sm)
		l, e := newLaneAttached(ctx, cfg.Server, u, a, t.TempDir(), false, cm, nil, logical, generation, 1)
		if e != nil {
			t.Fatal(e)
		}
		if e = clientStripe.attach(l); e != nil {
			l.close()
			t.Fatal(e)
		}
		clients = append(clients, l)
		go func() { done <- l.run() }()
	}
	for {
		n := 0
		for _, l := range clients {
			if l.ready.Load() {
				n++
			}
		}
		if n == 10 {
			break
		}
		select {
		case e := <-done:
			t.Fatalf("carrier failed before 20/20: %v", e)
		case <-ctx.Done():
			t.Fatalf("only %d/20 directions: %v", 2*n, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	serverLanes := make([]*lane, 0, 10)
	for i := 0; i < 10; i++ {
		select {
		case l := <-servers:
			serverLanes = append(serverLanes, l)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	traffic := make(chan error, 10)
	for _, l := range clients {
		go func(l *lane) {
			c, e := l.open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(number)})
			if e != nil {
				traffic <- e
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(30 * time.Second))
			payload := bytes.Repeat([]byte(l.account.ID), 32768)
			writeDone := make(chan error, 1)
			go func() {
				_, e := c.Write(payload)
				if e == nil {
					e = c.CloseWrite()
				}
				writeDone <- e
			}()
			received, e := io.ReadAll(c)
			if e == nil && !bytes.Equal(received, payload) {
				e = fmt.Errorf("%s corrupted echo: received=%d want=%d", l.account.ID, len(received), len(payload))
			}
			if e == nil {
				e = <-writeDone
			}
			traffic <- e
		}(l)
	}
	for i := 0; i < 10; i++ {
		select {
		case e := <-traffic:
			if e != nil {
				t.Fatal(e)
			}
		case e := <-done:
			t.Fatalf("carrier failed under echo: %v", e)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for _, l := range serverLanes {
		if !l.ready.Load() {
			t.Fatalf("server %s lost readiness", l.account.ID)
		}
		grants, e := l.data.ScanGrants(0, 256)
		if e != nil {
			t.Fatal(e)
		}
		for _, g := range grants {
			if g.Ref.Scope.Session == (wire.ID{99}) && (g.Closed || g.Released != (pool.Amount{}) || g.Limit != (pool.Amount{Bytes: 5 << 20, Messages: 80})) {
				t.Fatal("missing-wallet debt was refunded", g)
			}
		}
		if e = l.data.Audit(); e != nil {
			t.Fatal(e)
		}
	}
	t.Log("20/20 directions ready; ten concurrent bidirectional TCP echoes verified over TLS IMAP; old debt preserved")
	cancel()
	for i := 0; i < 20; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("carrier shutdown did not join")
		}
	}
}

func TestRetainedPartlySpentGrantReconcilesWithoutRefundingDATA(t *testing.T) {
	ctx := context.Background()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	dir := t.TempDir()
	s, err := ledger(dir, "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	wpath := filepath.Join(dir, "wallet.db")
	w, err := wallet.Open(wpath, u.ID, s.StoreID())
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: w.Session()}, pool.Amount{Bytes: dataLeaseBytes, Messages: dataLeaseMessages})
	if err != nil {
		t.Fatal(err)
	}
	offer, err := s.RemoteOffer(g.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(offer); err != nil {
		t.Fatal(err)
	}
	report, _, err := w.Spend(offer.Key, u.Up, []byte("retained unknown physical DATA"), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if err != nil {
		t.Fatal(err)
	}
	status, err := s.ApplyRemote(u.ID, s.Run(), report)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Acknowledge(status); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = ledger(dir, "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	epoch := cleanupFixtureEpoch(t)
	l := &lane{ctx: ctx, side: true, account: a, user: u, data: s, cipher: epoch, issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 16), maintenanceQueue: make(chan queuedControl, 16)}
	establishRecoveredServer(t, l)
	if err = l.reconcileUnpublishedGrants(); err != nil {
		t.Fatal(err)
	}
	if len(l.urgentQueue) != 1 {
		t.Fatal("partially spent old grant omitted from recovery", len(l.urgentQueue))
	}
	c, err := wire.DecodeControl((<-l.urgentQueue).Frame)
	if err != nil {
		t.Fatal(err)
	}
	recovery, ok := c.(wire.GrantRecovery)
	if !ok || recovery.Offer != offer {
		t.Fatal("wrong recovery allocation", c)
	}
	// A fresh device cannot manufacture a zero fence for another wallet's debt.
	foreign, err := wallet.OpenBound(filepath.Join(dir, "foreign.db"), s.StoreID(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	peer := &lane{ctx: ctx, account: a, user: u, wallet: foreign, cipher: epoch, urgentQueue: make(chan queuedControl, 16), maintenanceQueue: make(chan queuedControl, 16)}
	if err = peer.reconcileGrantOffer(recovery.Offer); err != nil {
		t.Fatal(err)
	}
	if len(peer.urgentQueue)+len(peer.maintenanceQueue) != 0 {
		t.Fatal("foreign history synthesized proof")
	}
	w, err = wallet.OpenBound(wpath, s.StoreID(), epoch)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	peer.wallet = w
	if err = peer.reconcileGrantOffer(recovery.Offer); err != nil {
		t.Fatal(err)
	}
	if len(peer.maintenanceQueue) != 1 {
		t.Fatal("lost final fence not recovered")
	}
	c, err = wire.DecodeControl((<-peer.maintenanceQueue).Frame)
	if err != nil {
		t.Fatal(err)
	}
	release, ok := c.(wire.StorageRelease)
	if !ok || release.Final != report.Total {
		t.Fatal("wrong final usage", c)
	}
	if _, err = s.ApplyRemote(u.ID, s.Run(), release); err != nil {
		t.Fatal(err)
	}
	held, err := s.Held(a.ID, u.ID)
	if err != nil || held != (pool.Amount{Bytes: report.Total.Bytes, Messages: report.Total.Messages}) {
		t.Fatal("unproven DATA refunded or unused quota not recovered", held, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestRetainedQuotaCannotManufactureFullJournalCapacity(t *testing.T) {
	ctx := context.Background()
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "up", Down: "down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	limit, _ := s.Limit(a.ID, u.ID)
	if _, err = s.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, limit); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: ctx, side: true, account: a, user: u, data: s, cipher: cleanupFixtureEpoch(t), stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 16)}
	establishRecoveredServer(t, l)
	if err = l.offer(); err != nil {
		t.Fatal(err)
	}
	held, err := s.Held(a.ID, u.ID)
	if err != nil || held != limit || l.ready.Load() || len(l.issued) != 0 {
		t.Fatal("full journal falsely activated or lost debt", held, err)
	}
}
