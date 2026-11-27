package mailstream

import (
	"context"
	"testing"
)

func TestDesktopFeedbackSpillBound(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, attached: id}
	a := &mailAccountState{link: l, id: id, ready: true, fast: make(chan mailWork, 8), bulk: make(chan mailWork, 8), interactive: make(chan mailWork, 8), controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}}
	l.accounts = []*mailAccountState{a}
	send := func(seq uint64) {
		t.Helper()
		if e := l.SendFeedback(ctx, header(sealed, id, seq)); e != nil {
			t.Fatal(e)
		}
	}
	send(1)
	if len(a.fast) != 1 || len(a.interactive) != 0 {
		t.Fatal("idle route changed")
	}
	send(2)
	send(3)
	if len(a.fast) != 2 || len(a.interactive) != 1 || !a.feedbackSpill.Load() {
		t.Fatal("missing bounded spill")
	}
	send(4)
	if len(a.fast) != 3 || len(a.interactive) != 1 {
		t.Fatal("second spill admitted")
	}
	w := <-a.interactive
	if !w.spill || w.target != a.controlWriter {
		t.Fatal("wrong lane")
	}
}

func TestDesktopFeedbackSpillReleasesStaleWork(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	p := &mailPipe{interactiveTags: make(chan struct{}, 2), done: make(chan struct{})}
	a := &mailAccountState{ctx: ctx, controlWriter: p, id: SessionID{2}, queuedBytes: 600, controlQueued: 1}
	a.feedbackSpill.Store(true)
	w := mailWork{spill: true, feedback: true, target: p, body: header(sealed, id, 1), charge: 600}
	if e := a.writeInteractiveBatch([]mailWork{w}); e != nil {
		t.Fatal(e)
	}
	if a.feedbackSpill.Load() || a.queuedBytes != 0 || a.controlQueued != 0 || len(p.interactiveTags) != 0 {
		t.Fatal("stale publication leaked reservation")
	}
}
