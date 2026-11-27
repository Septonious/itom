package mailstream

import (
	"context"
	"testing"
)

func TestMobileFeedbackUsesIndependentPublisher(t *testing.T) {
	ctx := context.Background()
	id := SessionID{1}
	l := &IMAPLink{ctx: ctx, attached: id}
	a := &mailAccountState{link: l, id: id, ready: true, fast: make(chan mailWork, 2), interactive: make(chan mailWork, 2), controlWriter: &mailPipe{interactiveTags: make(chan struct{}, 2)}}
	l.accounts = []*mailAccountState{a}
	if err := l.SendFeedback(ctx, header(sealed, id, 1)); err != nil || len(a.fast) != 1 || len(a.interactive) != 0 {
		t.Fatal("desktop path changed", err)
	}
	l.mobileFeedback.Store(true)
	if err := l.SendFeedback(ctx, header(sealed, id, 2)); err != nil || len(a.fast) != 1 || len(a.interactive) != 1 {
		t.Fatal("mobile feedback did not bypass DATA owner", err)
	}
	w := <-a.interactive
	if !w.feedback || w.target != a.controlWriter {
		t.Fatal("incorrect feedback attribution")
	}
	l.mobileFeedback.Store(false)
	if err := l.SendFeedback(ctx, header(sealed, id, 3)); err != nil || len(a.fast) != 2 {
		t.Fatal("desktop restoration", err)
	}
}
