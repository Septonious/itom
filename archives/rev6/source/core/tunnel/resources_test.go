package tunnel

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestSocketAdmissionAtomicLimitsAndCancellation(t *testing.T) {
	b := newSocketAdmission(5, 2, 1)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := b.acquire("a01")
			if err == nil {
				release()
				release()
			} else if !errors.Is(err, errLocalConnectionLimit) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if b.active != 0 || b.peak > 2 || len(b.accounts) != 0 {
		t.Fatal("socket reservations leaked or exceeded account budget")
	}
	var leases []func()
	for _, account := range []string{"a01", "a01", "a02", "a02", "a03"} {
		release, err := b.acquire(account)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, release)
	}
	if _, err := b.acquire("a04"); !errors.Is(err, errLocalConnectionLimit) {
		t.Fatal("aggregate limit bypassed", err)
	}
	for _, release := range leases {
		release()
	}
	a := newAccountAdmission(10, 5)
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.acquire(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := a.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled activation ignored", err)
	}
	a.release()
	a.release()
}

func TestInitialActivationOrderAndCanceledStandbys(t *testing.T) {
	b := newAccountAdmission(10, 5)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	order := make(chan int, 5)
	var wg sync.WaitGroup
	for i := 4; i >= 0; i-- {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.acquireInitial(ctx, i); err == nil {
				order <- i
			} else if !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		}()
	}
	first := <-order
	second := <-order
	if first+second != 1 {
		t.Fatal("initial accounts differed between endpoints", first, second)
	}
	b.release()
	if next := <-order; next != 2 {
		t.Fatal("standby skipped", next)
	}
	cancel()
	wg.Wait()
	b.release()
	b.release()
	if len(b.slots) != 0 {
		t.Fatal("canceled standby retained activation")
	}
}
