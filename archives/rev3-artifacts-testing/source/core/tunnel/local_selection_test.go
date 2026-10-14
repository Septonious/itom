package tunnel

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func cleanupFixtureEpoch(t *testing.T) *secure.Epoch {
	t.Helper()
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{1}}
	one, _ := secure.NewHandshake(binding, make([]byte, 32), true)
	two, _ := secure.NewHandshake(binding, make([]byte, 32), false)
	msg, _ := one.Write(nil)
	if err := two.Read(msg); err != nil {
		t.Fatal(err)
	}
	msg, _ = two.Write(nil)
	if err := one.Read(msg); err != nil {
		t.Fatal(err)
	}
	cipher, err := one.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

func TestLocalSelectionSkipsFullCarrierAndReservesWithoutOverbooking(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	cipher := cleanupFixtureEpoch(t)
	var lanes []*lane
	for _, id := range []string{"a01", "a02"} {
		l := stripeTestLane(g, id, true)
		defer l.cancel()
		l.account.Mail = mailbox.Config{Host: "mail.example"}
		l.user = servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
		l.cipher = cipher
		var err error
		l.data, err = ledger(t.TempDir(), "data", l.account, l.user, false)
		if err != nil {
			t.Fatal(err)
		}
		defer l.data.Close()
		if err = l.data.EnableAfterInventory(id, l.user.ID, l.data.Run()); err != nil {
			t.Fatal(err)
		}
		if err = g.attach(l); err != nil {
			t.Fatal(err)
		}
		lanes = append(lanes, l)
	}
	full, spare := lanes[0], lanes[1]
	scope := func(l *lane) journal.Scope {
		return journal.Scope{Account: l.account.ID, User: l.user.ID, Session: cipher.Context().Session}
	}
	if _, err := full.data.Issue(scope(full), pool.Amount{Bytes: 10 * 512 << 10, Messages: 7}); err != nil {
		t.Fatal(err)
	}
	before, _ := full.data.Held(full.account.ID, full.user.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	l, grant, err := g.takeLocalGrantForClass(ctx, nil, false, 512<<10)
	if err != nil || l != spare || grant.Limit.Bytes != 512<<10 {
		t.Fatal("full carrier stranded work", l, grant, err)
	}
	after, _ := full.data.Held(full.account.ID, full.user.ID)
	if before != after {
		t.Fatal("full debt changed without proof")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	reserved := []journal.Grant{grant}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, r, e := g.takeLocalGrantForClass(ctx, nil, false, 512<<10)
			if e == nil {
				mu.Lock()
				reserved = append(reserved, r)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	held, _, err := spare.data.HeldByOrigin(spare.account.ID, spare.user.ID)
	if err != nil || len(reserved) != 10 || held.Bytes != 10*512<<10 {
		t.Fatal("concurrent reservation overbooked origin share", len(reserved), held, err)
	}
	for _, r := range reserved {
		if err = spare.releaseReservedLocalGrant(r); err != nil {
			t.Fatal(err)
		}
	}
	held, _, err = spare.data.HeldByOrigin(spare.account.ID, spare.user.ID)
	if err != nil || held != (pool.Amount{}) {
		t.Fatal("unused reservations leaked", held, err)
	}
	if err = spare.data.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalReservationJoinsShutdownBeforeJournalUnlock(t *testing.T) {
	for _, writer := range []bool{false, true} {
		t.Run(fmt.Sprint(writer), func(t *testing.T) {
			g := newStripeGroup(wire.ID{1}, 1)
			l := stripeTestLane(g, "a01", true)
			l.account.Mail = mailbox.Config{Host: "mail.example"}
			l.user = servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			l.cipher = cleanupFixtureEpoch(t)
			path := filepath.Join(t.TempDir(), "data.db")
			cfg := ledgerConfig(l.account, l.user, dataProfile12)
			var err error
			l.data, err = journal.Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer l.data.Close()
			if err = l.data.EnableAfterInventory(l.account.ID, l.user.ID, l.data.Run()); err != nil {
				t.Fatal(err)
			}
			if err = g.attach(l); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			target, grant, err := g.takeLocalGrantForClass(ctx, nil, false, 512<<10)
			if err != nil || target != l {
				t.Fatal(target, err)
			}
			closed := make(chan struct{})
			go func() { l.close(); close(closed) }()
			<-l.ctx.Done()
			select {
			case <-closed:
				t.Fatal("shutdown unlocked journal with an unused reservation")
			default:
			}
			if _, err = l.data.Held(l.account.ID, l.user.ID); err != nil {
				t.Fatal("admitted writer lost its journal", err)
			}
			if writer {
				// Early validation must close the unused reservation before Done.
				l.writeBatchPhysicalWithReservations(l, nil, nil, &grant)
			} else if err = l.releaseReservedLocalGrant(grant); err != nil {
				t.Fatal(err)
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("shutdown did not join reservation owner")
			}
			reopened, err := journal.Open(path, cfg, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			held, err := reopened.Held(l.account.ID, l.user.ID)
			if err != nil || held != (pool.Amount{}) {
				t.Fatal("unused reservation survived journal unlock", held, err)
			}
			if err = reopened.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBalancedDuplexKeepsEstablishedLeasesAndBothJournalProfiles(t *testing.T) {
	for _, profile := range []pool.Amount{dataProfile8, dataProfile12} {
		t.Run(fmt.Sprint(profile.Bytes), func(t *testing.T) {
			cipher := cleanupFixtureEpoch(t)
			a := servercfg.Account{ID: "a01", Mail: mailbox.Config{Host: "mail.example"}}
			u := servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
			s, err := journal.Open(filepath.Join(t.TempDir(), "data.db"), ledgerConfig(a, u, profile), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err = s.EnableAfterInventory(a.ID, u.ID, s.Run()); err != nil {
				t.Fatal(err)
			}
			scope := journal.Scope{Account: a.ID, User: u.ID, Session: cipher.Context().Session}
			for _, limit := range []pool.Amount{{Bytes: fastLeaseBytes, Messages: fastLeaseMessages}, {Bytes: dataLeaseBytes, Messages: dataLeaseMessages}, {Bytes: dataLeaseBytes, Messages: dataLeaseMessages}} {
				if _, err = s.IssueRemote(scope, limit); err != nil {
					t.Fatal("baseline leases lost", err)
				}
			}
			l := &lane{ctx: context.Background(), side: true, account: a, user: u, data: s, cipher: cipher, stripe: newStripeGroup(wire.ID{1}, 1)}
			var grants []journal.Grant
			for {
				grant, e := l.tryLocalGrant(512<<10, false)
				if errors.Is(e, journal.ErrLimit) {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				grants = append(grants, grant)
			}
			local, remote, err := s.HeldByOrigin(a.ID, u.ID)
			want := 6
			if profile == dataProfile12 {
				want = 10
			}
			if err != nil || len(grants) != want || local.Bytes != uint64(want)*512<<10 || remote.Bytes != fastLeaseBytes+2*dataLeaseBytes || local.Bytes+remote.Bytes > profile.Bytes-localFastReserveBytes {
				t.Fatal("duplex capacity or profile changed", len(grants), local, remote, err)
			}
			effective, _ := s.Limit(a.ID, u.ID)
			if effective != profile {
				t.Fatal("profile silently migrated", effective, profile)
			}
			if err = s.Audit(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Journal admission can block while an independent carrier detach must proceed.
func TestLocalReservationAllowsDetachDuringJournalAdmission(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", true)
	defer l.cancel()
	l.account.Mail = mailbox.Config{Host: "mail.example"}
	l.user = servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	l.cipher = cleanupFixtureEpoch(t)
	var err error
	l.data, err = ledger(t.TempDir(), "data", l.account, l.user, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.data.Close()
	if err = l.data.EnableAfterInventory(l.account.ID, l.user.ID, l.data.Run()); err != nil {
		t.Fatal(err)
	}
	if err = g.attach(l); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	l.localGrantMu.Lock()
	l.physicalMu.Lock()
	result := make(chan error, 1)
	go func() {
		target, grant, err := g.takeLocalGrantForClass(ctx, nil, false, 512<<10)
		if err == nil {
			_ = target.releaseReservedLocalGrant(grant)
			err = errors.New("detached carrier returned")
		}
		result <- err
	}()
	// Reaching beginPhysicalWrite is a deterministic barrier before journal wait.
	deadline := time.Now().Add(time.Second)
	entered := false
	for time.Now().Before(deadline) {
		if !g.mu.TryLock() {
			entered = true
			break
		}
		g.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	l.physicalMu.Unlock()
	if !entered {
		cancel()
		l.localGrantMu.Unlock()
		<-result
		t.Fatal("selector did not enter admission")
	}
	detached := make(chan struct{})
	go func() { g.detach(l); close(detached) }()
	select {
	case <-detached:
	case <-time.After(200 * time.Millisecond):
		cancel()
		l.localGrantMu.Unlock()
		<-result
		<-detached
		t.Fatal("group lock held during journal admission")
	}
	cancel()
	l.localGrantMu.Unlock()
	if err = <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	l.physicalWriters.Wait()
	held, _, err := l.data.HeldByOrigin(l.account.ID, l.user.ID)
	if err != nil || held != (pool.Amount{}) {
		t.Fatal("reservation leaked", held, err)
	}
	if err = l.data.Audit(); err != nil {
		t.Fatal(err)
	}
}

type selectionWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *selectionWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestFastLocalAdmissionHasIndependentWakeWhenBulkConsumesOrdinaryWake(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "a01", true)
	defer l.cancel()
	l.account.Mail = mailbox.Config{Host: "mail.example"}
	l.user = servercfg.User{ID: "12345678", Up: "itom-u-up", Down: "itom-u-down"}
	l.cipher = cleanupFixtureEpoch(t)
	var err error
	l.data, err = ledger(t.TempDir(), "data", l.account, l.user, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.data.Close()
	if err = l.data.EnableAfterInventory(l.account.ID, l.user.ID, l.data.Run()); err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx := &selectionWaitContext{Context: base, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		target, grant, err := g.takeLocalGrantForClass(ctx, nil, true, 512<<10)
		if err == nil {
			if target != l {
				err = errors.New("wrong target")
			}
			if releaseErr := target.releaseReservedLocalGrant(grant); releaseErr != nil {
				err = releaseErr
			}
		}
		done <- err
	}()
	// Done evaluation proves the first scan saw no carriers and is entering wait.
	select {
	case <-ctx.entered:
	case <-base.Done():
		t.Fatal("fast selection never waited")
	}
	g.mu.Lock()
	g.lanes[l.account.ID] = l
	g.mu.Unlock()
	// The ordinary wake can already have been consumed by a bulk waiter.
	g.capacityNotice.signal()
	if err = <-done; err != nil {
		t.Fatal("bulk stole the only admission wake", err)
	}
}
