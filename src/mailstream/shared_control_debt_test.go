package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestDesktopSharedControlDebtAvoidsHiddenQueue(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, attached: id}
	var allowed [20]bool
	for i := range 3 {
		a := &mailAccountState{link: l, id: id, ready: true, fast: make(chan mailWork, 8), interactive: make(chan mailWork, 8), controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}}
		a.observeControlPublication(50 * time.Millisecond)
		l.accounts = append(l.accounts, a)
		allowed[i] = true
	}
	a, b, c := l.accounts[0], l.accounts[1], l.accounts[2]
	a.controlWriter.ordinaryControlTags.Store(2)
	b.fast <- mailWork{feedback: true}
	if got := l.idleInteractiveOrder(1, allowed, nil); got[0] != 2 {
		t.Fatalf("hidden debt preferred: %v", got)
	}
	got, err := l.send(ctx, header(sealed, id, 1), true, nil)
	if err != nil || got != c {
		t.Fatalf("idle publisher not selected: %v", err)
	}
	if a.idleInteractivePublisher(false) || b.idleInteractivePublisher(false) {
		t.Fatal("ordinary feedback counted as idle")
	}
	if !a.idleInteractivePublisher(true) || !b.idleInteractivePublisher(true) {
		t.Fatal("mobile behavior changed")
	}
	// A busy companion must still be admissible on the fallback pass.
	c.controlWriter.ordinaryControlTags.Store(1)
	if _, err = l.send(ctx, header(sealed, id, 2), true, nil); err != nil {
		t.Fatal(err)
	}
}
