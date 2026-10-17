package tunnel

import (
	"context"
	"errors"
	"testing"

	"itom.local/core/wire"
)

func TestRev4LogicalOwnerAnswersAfterPhysicalCarrierCancellation(t *testing.T) {
	physical, cancelPhysical := context.WithCancel(context.Background())
	logical, cancelLogical := context.WithCancel(context.Background())
	defer cancelLogical()
	owner := &lane{ctx: physical, ownerCtx: logical}
	cancelPhysical()
	for i := 0; i < 64; i++ {
		r := request{ctx: context.Background(), done: make(chan openResult, 1)}
		owner.answer(r, openResult{err: errors.New("test reply")})
		select {
		case reply := <-r.done:
			if reply.err == nil || reply.err.Error() != "test reply" {
				t.Fatal("wrong answer")
			}
		default:
			t.Fatal("physical cancellation discarded live logical answer")
		}
	}
	caller, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	r := request{ctx: caller, done: make(chan openResult, 1)}
	owner.answer(r, openResult{err: errors.New("late reply")})
	if len(r.done) != 0 {
		t.Fatal("answer delivered to an already canceled caller")
	}
}

func TestRev4OpenUsesSelectedCarrierWithRetainedLogicalActor(t *testing.T) {
	g := newStripeGroup(wire.ID{4}, 1)
	actor := stripeTestLane(g, "a01", false)
	selected := stripeTestLane(g, "a02", false)
	for _, l := range []*lane{actor, selected} {
		l.urgentQueue = make(chan queuedControl, 4)
		l.controlQueue = make(chan queuedControl, 4)
	}
	defer actor.cancel()
	defer selected.cancel()
	if err := g.attach(actor); err != nil {
		t.Fatal(err)
	}
	if err := g.attach(selected); err != nil {
		t.Fatal(err)
	}
	if err := g.register(1, actor); err != nil {
		t.Fatal(err)
	}
	g.streamAccount[1] = "a02"
	frame, err := wire.EncodeControl(1, wire.Connect{Host: "example.com", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	if err = g.sendStreamControl(context.Background(), actor, frame); err != nil {
		t.Fatal(err)
	}
	if len(selected.urgentQueue) != 1 || len(actor.urgentQueue) != 0 {
		t.Fatal("OPEN ignored selected physical carrier")
	}
	<-selected.urgentQueue
	selected.cancel()
	if err = g.sendStreamControl(context.Background(), actor, frame); err != nil {
		t.Fatal(err)
	}
	if len(actor.urgentQueue) != 1 {
		t.Fatal("unavailable selected carrier blocked OPEN fallback")
	}
}
