package tunnel

import (
	"context"
	"errors"
	"itom.local/core/wire"
	"testing"
	"time"
)

func TestRC9DataCleanupReadBudgetDoesNotBlockControlOrLeakOnCancellation(t *testing.T) {
	g := newStripeGroup(wire.ID{1}, 1)
	l := stripeTestLane(g, "read", false)
	defer l.cancel()
	l.stripe = g
	entered := make(chan struct{}, cleanupDataReadConcurrency)
	release := make(chan struct{})
	done := make(chan error, cleanupDataReadConcurrency)
	for i := 0; i < cleanupDataReadConcurrency; i++ {
		go func() {
			done <- l.cleanupRead(cleanupLocalData, func() error {
				entered <- struct{}{}
				<-release
				return errors.New("physical read failed")
			})
		}()
	}
	for i := 0; i < cleanupDataReadConcurrency; i++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("read slot unavailable")
		}
	}
	for _, kind := range []cleanupKind{cleanupRemote, cleanupLocalControl, cleanupPresence} {
		called := false
		if err := l.cleanupRead(kind, func() error { called = true; return nil }); err != nil || !called {
			t.Fatal("DATA reads blocked control/reclamation", kind, err)
		}
	}
	waiting := stripeTestLane(g, "wait", false)
	defer waiting.cancel()
	waiting.stripe = g
	canceled := make(chan error, 1)
	go func() {
		canceled <- waiting.cleanupRead(cleanupLocalData, func() error {
			t.Error("saturated reader budget admitted another DATA read")
			return nil
		})
	}()
	select {
	case err := <-canceled:
		t.Fatal("did not wait for a slot", err)
	case <-time.After(20 * time.Millisecond):
	}
	waiting.cancel()
	select {
	case err := <-canceled:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not wake blocked read")
	}
	close(release)
	for i := 0; i < cleanupDataReadConcurrency; i++ {
		<-done
	}
	if len(g.cleanupReads) != 0 {
		t.Fatal("failed reads leaked slots")
	}
	if err := l.cleanupRead(cleanupLocalData, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(g.cleanupReads) != 0 {
		t.Fatal("successful read leaked slot")
	}
}
