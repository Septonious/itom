package mailstream

import (
	"context"
	"testing"
)

func TestInteractiveIdlePublisherPreference(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, attached: id}
	for _, name := range []string{"first", "second", "third"} {
		l.accounts = append(l.accounts, &mailAccountState{link: l, config: MailAccount{ID: name}, id: id, ready: true,
			fast: make(chan mailWork, 8), interactive: make(chan mailWork, 8), controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}})
	}
	send := func(seq uint64, want int) {
		t.Helper()
		a, err := l.send(ctx, header(sealed, id, seq), true, nil)
		if err != nil || a != l.accounts[want] {
			t.Fatalf("route: got %v err %v, want %d", a, err, want)
		}
	}
	a, b, c := l.accounts[0], l.accounts[1], l.accounts[2]
	// First candidate queued, second candidate has both tag slots occupied.
	a.interactive <- mailWork{}
	b.controlWriter.interactiveTags <- struct{}{}
	b.controlWriter.interactiveTags <- struct{}{}
	send(1, 2)
	// All publishers busy: fall back without rejecting an otherwise valid packet.
	send(2, 1)
	// With empty queues and free tag slots, original round robin is retained.
	for _, x := range l.accounts {
		for len(x.interactive) > 0 {
			<-x.interactive
		}
		for len(x.controlWriter.interactiveTags) > 0 {
			<-x.controlWriter.interactiveTags
		}
	}
	send(3, 2)
	// Same-account probe remains pinned even with an idle alternative.
	if err := l.sendOnAccount(ctx, id, "third", header(sealed, id, 4), controlMessageLimit); err != nil || len(c.interactive) != 2 {
		t.Fatalf("pinned route: %v queue %d", err, len(c.interactive))
	}
	// Feedback deliberately retains its original account coalescing path.
	l.roundRobin = 2
	if err := l.SendFeedback(ctx, header(sealed, id, 5)); err != nil || len(c.fast) != 1 {
		t.Fatalf("feedback placement: %v queue %d", err, len(c.fast))
	}
}
