package mailstream

import (
	"context"
	"crypto/rand"
	"sync"
	"testing"
	"time"
)

type immediateFeedbackProbe struct {
	mu sync.Mutex
	prompt, ordinary [][]byte
	received chan struct{}
}

func (l *immediateFeedbackProbe) Send(context.Context, []byte, bool) error {
	return nil
}
func (l *immediateFeedbackProbe) Receive() <-chan []byte { return nil }
func (l *immediateFeedbackProbe) Attach(context.Context, SessionID) error { return nil }
func (l *immediateFeedbackProbe) Close() error { return nil }
func (l *immediateFeedbackProbe) SendFeedback(_ context.Context, wire []byte) error {
	l.mu.Lock()
	l.ordinary=append(l.ordinary,append([]byte(nil),wire...))
	l.mu.Unlock()
	select { case l.received<-struct{}{}: default: }
	return nil
}
func (l *immediateFeedbackProbe) SendPromptFeedback(_ context.Context, wire []byte) error {
	l.mu.Lock()
	l.prompt=append(l.prompt,append([]byte(nil),wire...))
	l.mu.Unlock()
	select { case l.received<-struct{}{}: default: }
	return nil
}
func makeImmediateFeedbackSession(t *testing.T, ctx context.Context, probe *immediateFeedbackProbe) *Session {
	t.Helper()
	key:=make([]byte,32)
	if _,e:=rand.Read(key);e!=nil {t.Fatal(e)}
	h,hello,e:=initiate(key,"prompt-receipt-test");if e!=nil {t.Fatal(e)}
	_,reply,e:=respond(key,"prompt-receipt-test",hello);if e!=nil {t.Fatal(e)}
	cipher,e:=h.finish(reply);if e!=nil {t.Fatal(e)}
	return &Session{ctx:ctx,link:probe,cipher:cipher,receipts:make(chan receiptRequest,16)}
}

func TestRC117InteractiveReceiptBypassesLoadedBulkFeedbackCoalescing(t *testing.T) {
	ctx,cancel:=context.WithCancel(context.Background())
	defer cancel()
	p:=&immediateFeedbackProbe{received:make(chan struct{},4)}
	s:=makeImmediateFeedbackSession(t,ctx,p)
	done:=make(chan struct{})
	go func(){ defer close(done); s.receiptLoop() }()
	s.receipts<-receiptRequest{sequence:1,bulk:true}
	s.receipts<-receiptRequest{sequence:2,bulk:false}
	select {
	case <-p.received:
	case <-time.After(500*time.Millisecond):t.Fatal("interactive receipt was not published")
	}
	p.mu.Lock()
	prompts,ordinary:=len(p.prompt),len(p.ordinary)
	p.mu.Unlock()
	if prompts!=1 || ordinary!=0 {
		t.Fatalf("mixed bulk/interactive receipt not urgent: immediate=%d ordinary=%d",prompts,ordinary)
	}
	cancel()
	select {case <-done:case <-time.After(time.Second):t.Fatal("receipt worker shutdown stalled")}
}

func TestRC117PureBulkFeedbackKeepsExistingBatchPath(t *testing.T) {
	ctx,cancel:=context.WithCancel(context.Background())
	defer cancel()
	p:=&immediateFeedbackProbe{received:make(chan struct{},4)}
	s:=makeImmediateFeedbackSession(t,ctx,p)
	done:=make(chan struct{})
	go func(){ defer close(done); s.receiptLoop() }()
	s.receipts<-receiptRequest{sequence:1,bulk:true}
	select {
	case <-p.received:
	case <-time.After(500*time.Millisecond):t.Fatal("bulk feedback was not published")
	}
	p.mu.Lock()
	prompts,ordinary:=len(p.prompt),len(p.ordinary)
	p.mu.Unlock()
	if prompts!=0 || ordinary!=1 {
		t.Fatalf("bulk feedback path changed: immediate=%d ordinary=%d",prompts,ordinary)
	}
	cancel()
	select {case <-done:case <-time.After(time.Second):t.Fatal("receipt worker shutdown stalled")}
}
