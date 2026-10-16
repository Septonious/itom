package tunnel

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"itom.local/core/socks"
	"itom.local/core/wire"
)

func TestOpenReservationBoundAndTerminalRelease(t *testing.T) {
	g := newStripeGroup(wire.ID{8}, 5)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	var ids []uint64
	for i := 0; i < runtimeOwnerStreams; i++ {
		id, err := g.allocate(l)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	before := g.nextID
	_, err := g.allocate(l)
	var capacity socks.OpenError
	if !errors.As(err, &capacity) || capacity.Code != wire.ReasonCapacity {
		t.Fatal("pending OPEN exceeds owner socket bound", err)
	}
	if g.nextID != before {
		t.Fatal("rejected OPEN consumes session ID")
	}
	g.retire(ids[0], l)
	g.retire(ids[0], l)
	if _, err = g.allocate(l); err != nil {
		t.Fatal("duplicate retirement lost capacity", err)
	}
	g.releaseOwner(l)
	if g.activeStreams() != 0 {
		t.Fatal("release retained routes")
	}
	if _, err = g.allocate(l); err != nil {
		t.Fatal("release retained owner capacity", err)
	}
}

func TestOpenBurstPendingReservationsUseSiblingOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	g := newStripeGroup(wire.ID{4}, 3)
	fast := stripeTestLane(g, "a01", false)
	defer fast.cancel()
	sibling := stripeTestLane(g, "a02", false)
	defer sibling.cancel()
	for _, l := range []*lane{fast, sibling} {
		l.stripe = g
		l.ownerCtx = ctx
		l.ownerStarted = true
		l.actorReady = make(chan struct{})
		close(l.actorReady)
		l.requests = make(chan request)
		l.lastProgress.Store(time.Now().UnixNano())
		g.logicalOwners[l.account.ID] = l
		if err := g.attach(l); err != nil {
			t.Fatal(err)
		}
	}
	fast.rtt.Store(30)
	sibling.rtt.Store(1400)
	for i := 0; i < runtimeOwnerStreams; i++ {
		if _, err := g.allocate(fast); err != nil {
			t.Fatal(err)
		}
	}
	chosen := make(chan *lane, 2)
	var wg sync.WaitGroup
	for _, l := range []*lane{fast, sibling} {
		wg.Add(1)
		go func(l *lane) {
			defer wg.Done()
			select {
			case req := <-l.requests:
				chosen <- l
				app, peer := pipes()
				defer peer.Close()
				l.answer(req, openResult{socket: app})
			case <-ctx.Done():
			}
		}(l)
	}
	defer wg.Wait()
	c := &Client{ctx: ctx, stripe: g, lanes: map[string]*lane{"a01": fast, "a02": sibling}}
	s, _, err := c.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case l := <-chosen:
		if l != sibling {
			t.Fatal("full fast owner selected despite free sibling")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
}

func TestClientOwnerCapacityRejectsOnlyNewSocket(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := newStripeGroup(wire.ID{1}, 2)
	l := stripeTestLane(g, "a01", false)
	defer l.cancel()
	l.stripe = g
	l.ownerCtx = ctx
	l.ownerStarted = true
	l.actorReady = make(chan struct{})
	close(l.actorReady)
	l.lastProgress.Store(time.Now().UnixNano())
	g.logicalOwners[l.account.ID] = l
	if err := g.attach(l); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < runtimeOwnerStreams; i++ {
		if _, err := g.allocate(l); err != nil {
			t.Fatal(err)
		}
	}
	c := &Client{ctx: ctx, stripe: g, lanes: map[string]*lane{"a01": l}}
	_, addr, err := c.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: 8080})
	var capacity socks.OpenError
	if !errors.As(err, &capacity) || capacity.Code != wire.ReasonCapacity || addr != (netip.AddrPort{}) {
		t.Fatal("new socket wasn't locally rejected", err)
	}
	if ctx.Err() != nil || l.ctx.Err() != nil || g.activeStreams() != runtimeOwnerStreams || g.stopped {
		t.Fatal("capacity killed existing streams")
	}
}
