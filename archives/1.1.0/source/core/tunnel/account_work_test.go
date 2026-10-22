package tunnel

import (
	"context"
	"errors"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

func TestAccountWorkCancellationAndClassIsolation(t *testing.T) {
	var b accountWorkBudget
	ctx := context.Background()
	maintenance, err := b.acquire(ctx, workMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		release, err := b.acquire(blocked, workMaintenance)
		if release != nil {
			release()
		}
		result <- err
	}()
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		queued := len(b.queues[workMaintenance])
		b.mu.Unlock()
		if queued == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiter not queued")
		}
		time.Sleep(time.Millisecond)
	}
	urgent, err := b.acquire(ctx, workUrgent)
	if err != nil {
		t.Fatal(err)
	}
	urgent()
	receive, err := b.acquire(ctx, workReceive)
	if err != nil {
		t.Fatal(err)
	}
	receive()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	maintenance()
	maintenance() // shared lease completion is idempotent
	next, err := b.acquire(ctx, workMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	next()
	b.mu.Lock()
	defer b.mu.Unlock()
	for class := workUrgent; class < workClassCount; class++ {
		if b.busy[class] != 0 || len(b.queues[class]) != 0 {
			t.Fatal("leaked account lease", class)
		}
	}
}

func TestAccountMaintenanceFIFOAcrossUsers(t *testing.T) {
	gate := &accountDialGate{}
	a, b := &laneMail{dialGate: gate}, &laneMail{dialGate: gate}
	held, _ := gate.work.acquire(context.Background(), workMaintenance)
	order := make(chan int, 2)
	for i, m := range []*laneMail{a, b} {
		go func(i int, m *laneMail) {
			if err := m.command(context.Background(), workMaintenance, func() error { order <- i; return nil }); err != nil {
				t.Error(err)
			}
		}(i, m)
		deadline := time.Now().Add(time.Second)
		for {
			gate.work.mu.Lock()
			n := len(gate.work.queues[workMaintenance])
			gate.work.mu.Unlock()
			if n == i+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("queue")
			}
			time.Sleep(time.Millisecond)
		}
	}
	held()
	for want := 0; want < 2; want++ {
		select {
		case got := <-order:
			if got != want {
				t.Fatal("fairness", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("starved user")
		}
	}
}

func TestProofCacheDeduplicatesOnlyActualPhysicalObservations(t *testing.T) {
	raw, _ := mailbox.Encode([]byte("body proof"))
	row := mailbox.Message{Folder: "mail", Validity: 17, UID: 2, MIME: raw}
	rows := make([]mailbox.Message, 700)
	for i := range rows {
		rows[i] = row
	}
	merged, err := mergeProofCache(nil, rows)
	if err != nil || len(merged) != 1 {
		t.Fatal("duplicate evidence exhausted cache", len(merged), err)
	}
	second := row
	second.Validity++
	merged, err = mergeProofCache(merged, []mailbox.Message{second})
	if err != nil || len(merged) != 2 {
		t.Fatal("validity identities merged", err)
	}
	second = row
	second.MIME, _ = mailbox.Encode([]byte("replaced body"))
	if _, err = mergeProofCache(merged, []mailbox.Message{second}); !errors.Is(err, mailbox.ErrChanged) {
		t.Fatal("body substitution accepted", err)
	}
}

func TestSharedControlCommandGivesUrgentNextTurnAndCannotStarveGCOrBulk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &laneMail{}
	held := make(chan struct{})
	entered := make(chan struct{})
	order := make(chan accountWorkClass, 3)
	done := make(chan error, 4)
	go func() { done <- m.controlCommand(ctx, workData, func() error { close(entered); <-held; return nil }) }()
	<-entered
	for _, class := range []accountWorkClass{workData, workMaintenance, workUrgent} {
		class := class
		go func() { done <- m.controlCommand(ctx, class, func() error { order <- class; return nil }) }()
	}
	for {
		m.baseWork.mu.Lock()
		n := len(m.baseWork.queues[workData]) + len(m.baseWork.queues[workMaintenance]) + len(m.baseWork.queues[workUrgent])
		m.baseWork.mu.Unlock()
		if n == 3 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("command waiters never queued")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(held)
	for _, want := range []accountWorkClass{workUrgent, workMaintenance, workData} {
		select {
		case got := <-order:
			if got != want {
				t.Fatal("shared socket lost class fairness", got, want)
			}
		case <-ctx.Done():
			t.Fatal("class starved")
		}
	}
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
