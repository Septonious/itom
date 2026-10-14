package mailbox

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

type unusedTransport struct{ Transport }

func newDispatch(t *testing.T, capacity int) *Dispatcher {
	t.Helper()
	d, e := NewDispatcher(context.Background(), unusedTransport{}, DispatchConfig{Routes: []RouteConfig{{User: "alice", Folders: []string{"a-up", "a-down"}}, {User: "bob", Folders: []string{"b-up", "b-down"}}}, QueuePerUser: capacity})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(d.Close)
	return d
}
func queued(t *testing.T, d *Dispatcher, index, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		length := len(d.queues[index].pending)
		d.mu.Unlock()
		if length == n {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("queue did not reach expected size")
}
func TestDispatcherFairnessCapacityAndFolderIsolation(t *testing.T) {
	d := newDispatch(t, 2)
	a, _ := d.Route("alice")
	b, _ := d.Route("bob")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	done := make(chan error, 5)
	submit := func(r *Route, folder, name string, hold bool) {
		done <- r.submit(ctx, folder, func(context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			if hold {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
	}
	go submit(a, "a-up", "a1", true)
	<-started
	go submit(a, "a-down", "a2", false)
	queued(t, d, 0, 1)
	go submit(a, "a-up", "a3", false)
	queued(t, d, 0, 2)
	if e := a.submit(ctx, "a-up", func(context.Context) error { t.Error("overfull request ran"); return nil }); e != ErrBusy {
		t.Fatal(e)
	}
	if e := a.Ensure(ctx, "b-up"); e != ErrScope {
		t.Fatal(e)
	}
	if _, e := d.Route("mallory"); e != ErrScope {
		t.Fatal(e)
	}
	go submit(b, "b-up", "b1", false)
	queued(t, d, 1, 1)
	go submit(b, "b-down", "b2", false)
	queued(t, d, 1, 2)
	close(release)
	for i := 0; i < 5; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if !reflect.DeepEqual(order, []string{"a1", "b1", "a2", "b2", "a3"}) {
		t.Fatal(order)
	}
}
func TestDispatcherQueuedCancellationDoesNotWaitForBusyAccount(t *testing.T) {
	d := newDispatch(t, 1)
	a, _ := d.Route("alice")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- a.submit(ctx, "a-up", func(context.Context) error {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		})
	}()
	<-started
	canceled, cancelQueued := context.WithCancel(ctx)
	second := make(chan error, 1)
	go func() {
		second <- a.submit(canceled, "a-up", func(context.Context) error { t.Error("canceled work ran"); return nil })
	}()
	queued(t, d, 0, 1)
	cancelQueued()
	select {
	case e := <-second:
		if !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("queued cancel blocked")
	}
	close(release)
	<-first
}
func TestDispatcherRunningAppendJoinsUnknownOutcome(t *testing.T) {
	d := newDispatch(t, 1)
	a, _ := d.Route("alice")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- a.submit(ctx, "a-up", func(c context.Context) error {
			close(started)
			<-c.Done()
			close(canceled)
			<-release
			return ErrUnknown
		})
	}()
	<-started
	cancel()
	<-canceled
	select {
	case e := <-done:
		t.Fatal("returned before completion", e)
	default:
	}
	close(release)
	if e := <-done; !errors.Is(e, ErrUnknown) {
		t.Fatal(e)
	}
}
func TestDispatcherCloseCancelsAndJoins(t *testing.T) {
	d := newDispatch(t, 1)
	a, _ := d.Route("alice")
	b, _ := d.Route("bob")
	started := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		done <- a.submit(context.Background(), "a-up", func(c context.Context) error { close(started); <-c.Done(); return ErrUnknown })
	}()
	<-started
	go func() {
		done <- b.submit(context.Background(), "b-up", func(context.Context) error { t.Error("queued work after shutdown"); return nil })
	}()
	queued(t, d, 1, 1)
	d.Close()
	seen := map[error]bool{(<-done): true, (<-done): true}
	if !seen[ErrUnknown] || !seen[ErrStopped] {
		t.Fatal(seen)
	}
	if e := a.Ensure(context.Background(), "a-up"); e != ErrStopped {
		t.Fatal(e)
	}
}
func TestDispatcherRejectsOverlappingFolders(t *testing.T) {
	_, e := NewDispatcher(context.Background(), unusedTransport{}, DispatchConfig{Routes: []RouteConfig{{User: "a", Folders: []string{"same"}}, {User: "b", Folders: []string{"same"}}}, QueuePerUser: 1})
	if e != ErrScope {
		t.Fatal(e)
	}
}
