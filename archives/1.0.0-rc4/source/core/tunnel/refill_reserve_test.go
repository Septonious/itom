package tunnel

import (
	"context"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestFullDuplexBulkPreservesRemoteRefillAndFastBytes(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	binding := secure.Binding{Server: wire.ID{1}, User: u.ID, Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
	two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
	msg, _ := one.Write(nil)
	if err = two.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, _ = two.Write(nil)
	if err = one.Read(msg); err != nil {
		t.Fatal(err)
	}
	cipher, err := one.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: binding.Session}
	if _, err = s.Issue(scope, pool.Amount{Bytes: 10 << 20, Messages: 1}); err != nil {
		t.Fatal(err)
	}
	l := &lane{data: s, account: a, user: u, side: true, stripe: newStripeGroup(wire.ID{1}, 1), cipher: cipher}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = l.waitLocalGrantContextClass(ctx, 512<<10, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bulk consumed remote refill headroom: %v", err)
	}
	before, _ := s.Held(a.ID, u.ID)
	fast, err := l.waitLocalGrantContextClass(context.Background(), 512<<10, true)
	if err != nil {
		t.Fatal("fast reserve unavailable", err)
	}
	after, _ := s.Held(a.ID, u.ID)
	if after.Bytes != before.Bytes+512<<10 {
		t.Fatal("unexpected debt mutation", before, after)
	}
	if err = s.CloseGrant(fast.Ref); err != nil {
		t.Fatal(err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestInboundLeasesLeaveUsableOutboundBulkShare(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	binding := secure.Binding{Server: wire.ID{1}, User: u.ID, Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
	two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
	msg, _ := one.Write(nil)
	if err = two.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, _ = two.Write(nil)
	if err = one.Read(msg); err != nil {
		t.Fatal(err)
	}
	cipher, err := one.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: binding.Session}
	if _, err = s.IssueRemote(scope, pool.Amount{Bytes: 8 << 20, Messages: 128}); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cipher, stripe: newStripeGroup(wire.ID{1}, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err = l.waitLocalGrantContextClass(ctx, 512<<10, false); err != nil {
		t.Fatal("inbound allocation consumed outbound share", err)
	}
	if err = l.issueRemoteGrant(pool.Amount{Bytes: dataLeaseBytes, Messages: dataLeaseMessages}, false); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("inbound allocation exceeded its bounded share", err)
	}
	local, remote, err := s.HeldByOrigin(a.ID, u.ID)
	if err != nil || local.Bytes != 512<<10 || remote.Bytes != 8<<20 {
		t.Fatal("origin admission changed durable debt", local, remote, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestControlDebtPausesBulkRefillButPreservesFastLease(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	binding := secure.Binding{Server: wire.ID{1}, User: u.ID, Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
	two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
	msg, _ := one.Write(nil)
	if err = two.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, _ = two.Write(nil)
	if err = one.Read(msg); err != nil {
		t.Fatal(err)
	}
	cipher, err := one.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	control, err := ledger(t.TempDir(), "control", a, u, true)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if err = control.EnableAfterInventory(a.ID, u.ID, control.Run()); err != nil {
		t.Fatal(err)
	}
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: binding.Session}
	if _, err = control.Issue(scope, pool.Amount{Bytes: 64 * 128, Messages: 64}); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, control: control, cipher: cipher, stripe: newStripeGroup(wire.ID{1}, 1), issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 4)}
	if err = l.issueRemoteGrant(pool.Amount{Bytes: dataLeaseBytes, Messages: dataLeaseMessages}, false); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("bulk refill consumed critical control headroom", err)
	}
	if err = l.issueRemoteGrant(pool.Amount{Bytes: fastLeaseBytes, Messages: fastLeaseMessages}, true); err != nil {
		t.Fatal("fast lease lost service during bulk backpressure", err)
	}
	debt, err := control.Held(a.ID, u.ID)
	if err != nil || debt.Messages != 64 {
		t.Fatal("backpressure refunded durable control debt", debt, err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
	if err = control.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestFencedBulkReplacementFitsWithoutBorrowingFastReserve(t *testing.T) {
	a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
	u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	s, err := ledger(t.TempDir(), "data", a, u, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
		t.Fatal(err)
	}
	cipher := cleanupFixtureEpoch(t)
	scope := journal.Scope{Account: a.ID, User: u.ID, Session: cipher.Context().Session}
	if _, err = s.Issue(scope, pool.Amount{Bytes: 5 << 20, Messages: 10}); err != nil {
		t.Fatal(err)
	}
	l := &lane{ctx: context.Background(), data: s, account: a, user: u, side: true, stripe: newStripeGroup(wire.ID{1}, 1), cipher: cipher, issued: map[wire.QuotaKey]bool{}, issuedFast: map[wire.QuotaKey]bool{}, urgentQueue: make(chan queuedControl, 4)}
	if err = l.issueRemoteGrant(pool.Amount{Bytes: fastLeaseBytes, Messages: fastLeaseMessages}, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = l.issueRemoteGrant(pool.Amount{Bytes: dataLeaseBytes, Messages: dataLeaseMessages}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err = l.offerBulkReplacement(); err != nil {
		t.Fatal(err)
	}
	if fast, bulk := l.issuedGrantCounts(); fast != 1 || bulk != 3 {
		t.Fatal(fast, bulk)
	}
	before, _ := s.Held(a.ID, u.ID)
	if err = l.offerBulkReplacement(); err != nil {
		t.Fatal(err)
	}
	if err = l.issueRemoteGrant(pool.Amount{Bytes: dataLeaseBytes, Messages: dataLeaseMessages}, false); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("fourth bulk lease admitted", err)
	}
	after, _ := s.Held(a.ID, u.ID)
	if after != before || after.Bytes != uint64(11.5*(1<<20)) {
		t.Fatal(before, after)
	}
	if _, err = l.tryLocalGrant(512<<10, true); err != nil {
		t.Fatal("lost fast reserve", err)
	}
	if _, err = l.tryLocalGrant(1, false); !errors.Is(err, journal.ErrLimit) {
		t.Fatal("bulk borrowed fast reserve", err)
	}
	if err = s.Audit(); err != nil {
		t.Fatal(err)
	}
	if local, remote := duplexDataShares(8 << 20); local != 3.5*(1<<20) || remote != 4.5*(1<<20) {
		t.Fatal("legacy capacity changed", local, remote)
	}
}
