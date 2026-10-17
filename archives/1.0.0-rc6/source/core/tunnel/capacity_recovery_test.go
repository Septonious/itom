package tunnel

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"itom.local/core/mailbox"
)

// One scenario covers queued exact-once ownership, optional server-worker
// suspension, paced recovery, and client capacity refused during activation.
func TestDATAWriterCapacityRecoversWithoutCarrierRestart(t *testing.T) {
	t.Run("shared_server_queue", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		state := &capacityAppendState{gate: make(chan struct{}), entered: make(chan struct{}, 4), seen: map[byte]int{}}
		var allowExtra atomic.Bool
		var dials, refused atomic.Int32
		r := newAccountRuntime(ctx, []string{"u1", "u2"}, func(callCtx context.Context) (appendClient, error) {
			n := dials.Add(1)
			if n > 1 && !allowExtra.Load() {
				refused.Add(1)
				return nil, errors.New("IMAP_AUTH_OR_GREETING_FAILED")
			}
			state.mu.Lock()
			state.open++
			state.peak = max(state.peak, state.open)
			state.mu.Unlock()
			return &capacityAppendClient{state: state}, nil
		})
		defer r.Close()
		wakes := make(chan int, 4)
		m := &laneMail{runtime: r}
		m.SetDataWake(func() {
			select {
			case wakes <- r.Parallelism():
			default:
			}
		})
		results := make(chan error, 3)
		go func() { results <- r.Append(ctx, "u1", "data", []byte{1}) }()
		select {
		case <-state.entered:
		case <-ctx.Done():
			t.Fatal("first DATA did not enter", ctx.Err())
		}
		go func() { results <- r.Append(ctx, "u1", "data", []byte{2}) }()
		awaitCapacity(t, ctx, func() bool { return r.Parallelism() == 1 && refused.Load() > 0 }, "server did not suspend optional capacity")
		snapshot := r.snapshot()
		if snapshot.BusyWorkers != 1 || snapshot.QueuedRequests != 1 || snapshot.PendingBytes != 2 || snapshot.Failed != 0 {
			t.Fatal("requeued BODY ownership/bounds changed", snapshot)
		}
		go func() { results <- r.Append(ctx, "u2", "data", []byte{3}) }()
		allowExtra.Store(true)
		awaitCapacity(t, ctx, func() bool { return r.Parallelism() == 2 }, "server DATA worker remained permanently retired")
		close(state.gate)
		for i := 0; i < 3; i++ {
			select {
			case err := <-results:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("DATA did not drain after cap cleared", ctx.Err())
			}
		}
		state.mu.Lock()
		if state.peak != 2 || len(state.seen) != 3 || state.seen[1] != 1 || state.seen[2] != 1 || state.seen[3] != 1 {
			t.Error("physical capacity/exact-once mismatch", state.peak, state.seen)
		}
		state.mu.Unlock()
		snapshot = r.snapshot()
		if snapshot.Completed != 3 || snapshot.Failed != 0 || snapshot.PendingBytes != 0 || snapshot.QueuedRequests != 0 || snapshot.BusyWorkers != 0 {
			t.Fatal("runtime did not return to bounded idle", snapshot)
		}
		var down, up bool
		for len(wakes) > 0 {
			switch <-wakes {
			case 1:
				down = true
			case 2:
				up = true
			}
		}
		if !down || !up {
			t.Fatal("capacity changes did not wake striped waiters", down, up)
		}
		t.Logf("refused_dials=%d recovered_workers=2 append_calls=3 duplicates=0 failed=0", refused.Load())
	})

	t.Run("first_server_socket_refused", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		state := &capacityAppendState{gate: make(chan struct{}), entered: make(chan struct{}, 4), seen: map[byte]int{}}
		close(state.gate)
		var allowed atomic.Bool
		var refused atomic.Int32
		r := newAccountRuntime(ctx, []string{"u1"}, func(callCtx context.Context) (appendClient, error) {
			if !allowed.Load() {
				refused.Add(1)
				return nil, errors.New("IMAP_AUTH_OR_GREETING_FAILED")
			}
			state.mu.Lock()
			state.open++
			state.peak = max(state.peak, state.open)
			state.mu.Unlock()
			return &capacityAppendClient{state: state}, nil
		})
		defer r.Close()
		result := make(chan error, 1)
		go func() { result <- r.Append(ctx, "u1", "data", []byte{4}) }()
		awaitCapacity(t, ctx, func() bool { return refused.Load() >= 2 }, "initial provider cap was not exercised")
		select {
		case err := <-result:
			t.Fatal("pre-APPEND refusal created an unknown outcome instead of backpressure", err)
		default:
		}
		snapshot := r.snapshot()
		if snapshot.PendingBytes != 1 || snapshot.Failed != 0 || snapshot.Completed != 0 {
			t.Fatal("first-writer cap lost bounded BODY ownership", snapshot)
		}
		allowed.Store(true)
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("first DATA never resumed", ctx.Err())
		}
		awaitCapacity(t, ctx, func() bool { return r.Parallelism() == 2 }, "optional capacity failed to recover after mandatory writer")
		state.mu.Lock()
		if state.seen[4] != 1 || len(state.seen) != 1 || state.peak != 2 {
			t.Error("first-writer replay/capacity mismatch", state.seen, state.peak)
		}
		state.mu.Unlock()
		if s := r.snapshot(); s.Completed != 1 || s.Failed != 0 || s.PendingBytes != 0 {
			t.Fatal("refusal generated failed APPEND accounting", s)
		}
		t.Logf("first_writer_refusals=%d physical_append_calls=1 unknown_outcomes=0 recovered_workers=2", refused.Load())
	})
	t.Run("client_refused_during_activation", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cm, sm := tlsMailPair(t, ctx, []string{"itom-up", "itom-down"})
		m := cm.(*laneMail)
		dial := m.dial
		var calls atomic.Int32
		var allowExtra atomic.Bool
		m.dial = func(callCtx context.Context) (*mailbox.Client, error) {
			if calls.Add(1) >= 4 && !allowExtra.Load() {
				return nil, errors.New("IMAP_AUTH_OR_GREETING_FAILED")
			}
			return dial(callCtx)
		}
		if err := m.Activate(ctx, "itom-down"); err != nil {
			t.Fatal(err)
		}
		if m.DataParallelism() != 1 {
			t.Fatal("optional activation refusal did not degrade to one DATA writer")
		}
		base, reader, cleaner := m.Client, m.reader, m.cleaner
		wake := make(chan struct{}, 1)
		m.SetDataWake(func() {
			select {
			case wake <- struct{}{}:
			default:
			}
		})
		allowExtra.Store(true)
		awaitCapacity(t, ctx, func() bool { return m.DataParallelism() == 2 }, "missing activation writer was never repaired")
		if m.Client != base || m.reader != reader || m.cleaner != cleaner {
			t.Fatal("DATA recovery replaced control/reader/cleaner")
		}
		select {
		case <-wake:
		case <-ctx.Done():
			t.Fatal("client capacity recovery did not wake striped waiters")
		}
		raw, err := mailbox.Encode([]byte("recovered-data-capacity"))
		if err != nil {
			t.Fatal(err)
		}
		if err := m.AppendData(ctx, "itom-up", raw); err != nil {
			t.Fatal(err)
		}
		rows, _, err := sm.Page(ctx, "itom-up", mailbox.Cursor{})
		if err != nil || len(rows) != 1 {
			t.Fatal("recovered DATA did not arrive", len(rows), err)
		}
		body, err := mailbox.Decode(rows[0].MIME)
		if err != nil || string(body) != "recovered-data-capacity" {
			t.Fatal("recovered DATA corrupted", err)
		}
		if m.connectionCount() != 5 {
			t.Fatal("repair positions reported as sockets", m.connectionCount())
		}
		if err := m.Close(); err != nil {
			t.Fatal(err)
		}
		if m.DataAvailable() || m.connectionCount() != 0 {
			t.Fatal("closed DATA capacity remained available")
		}
		t.Log("client_workers=2 original_control_reader_cleaner_preserved=true recovered_body_verified=true")
	})
}

func awaitCapacity(t *testing.T, ctx context.Context, ready func() bool, reason string) {
	t.Helper()
	timer := time.NewTicker(5 * time.Millisecond)
	defer timer.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatal(reason, ctx.Err())
		case <-timer.C:
		}
	}
}

type capacityAppendState struct {
	mu         sync.Mutex
	gate       chan struct{}
	entered    chan struct{}
	seen       map[byte]int
	open, peak int
}
type capacityAppendClient struct {
	state *capacityAppendState
	once  sync.Once
}

func (c *capacityAppendClient) Append(ctx context.Context, _ string, raw []byte) error {
	select {
	case c.state.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.state.gate:
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.seen[raw[0]]++
	if c.state.seen[raw[0]] != 1 {
		return errors.New("duplicate physical APPEND")
	}
	return nil
}
func (c *capacityAppendClient) Close() error {
	c.once.Do(func() { c.state.mu.Lock(); c.state.open--; c.state.mu.Unlock() })
	return nil
}
