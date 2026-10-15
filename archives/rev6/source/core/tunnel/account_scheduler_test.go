package tunnel

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAccountSchedulerBalancesMIMEBytesAcrossUsers(t *testing.T) {
	r := &accountRuntime{ctx: context.Background(), users: []string{"big", "small"}, queues: map[string][]accountAppend{}, deficit: map[string]int{}}
	r.cond = sync.NewCond(&r.mu)
	for i := 0; i < 8; i++ {
		r.queues["big"] = append(r.queues["big"], accountAppend{user: "big", raw: make([]byte, 512<<10)})
	}
	for i := 0; i < 512; i++ {
		r.queues["small"] = append(r.queues["small"], accountAppend{user: "small", raw: make([]byte, 8<<10)})
	}
	served := map[string]int{}
	for i := 0; i < 260; i++ {
		req, ok := r.take()
		if !ok {
			t.Fatal("scheduler stopped")
		}
		served[req.user] += len(req.raw)
	}
	if delta := served["big"] - served["small"]; delta < -(512<<10) || delta > 512<<10 {
		t.Fatalf("per-message fairness starved small user: %v", served)
	}
}

func TestAccountDialAdmissionHonorsCooldownAndCancellation(t *testing.T) {
	g := newAccountDialGate()
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = g.run(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	cancel()
	called := false
	if err := g.run(ctx, func() error { called = true; return nil }); err != context.Canceled || called {
		t.Fatal("canceled dial crossed admission", err)
	}
	close(release)
	wg.Wait()
	g.mu.Lock()
	g.next = time.Now().Add(time.Hour)
	g.mu.Unlock()
	short, stop := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer stop()
	if err := g.run(short, func() error { called = true; return nil }); err != context.DeadlineExceeded || called {
		t.Fatal("cooldown ignored caller deadline", err)
	}
}

type idleDeadAppendClient struct {
	ready bool
	calls int
}

func (c *idleDeadAppendClient) Ready() bool                                  { return c.ready }
func (c *idleDeadAppendClient) Close() error                                 { c.ready = false; return nil }
func (c *idleDeadAppendClient) Append(context.Context, string, []byte) error { c.calls++; return nil }

func TestAccountWorkerRedialsObservedDeadSocketWithoutResendingUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var mu sync.Mutex
	var clients []*idleDeadAppendClient
	r := newAccountRuntime(ctx, []string{"user"}, func(context.Context) (appendClient, error) {
		mu.Lock()
		defer mu.Unlock()
		c := &idleDeadAppendClient{ready: true}
		clients = append(clients, c)
		return c, nil
	})
	defer r.Close()
	if err := r.Append(ctx, "user", "data", []byte("one")); err != nil {
		t.Fatal(err)
	}
	// A deterministic server idle-close reproduction is exercised separately
	// through the real TLS adapter. Here force all current worker generations dead.
	mu.Lock()
	for _, c := range clients {
		c.ready = false
	}
	mu.Unlock()
	if err := r.Append(ctx, "user", "data", []byte("two")); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	total := 0
	for _, c := range clients {
		total += c.calls
	}
	if total != 2 {
		t.Fatal("extra publication", total)
	}
}
