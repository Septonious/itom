package transport_test

import (
	"errors"
	"math/rand"
	"testing"
	"time"

	"itom.local/core/transport"
	"itom.local/core/wire"
)

func windowScope() transport.WindowScope {
	return transport.WindowScope{Session: wire.ID{101}, Generation: 7, Stream: 1, Direction: transport.ClientToServer}
}

func TestWindowRecallRetainsBackingForOldFlightUntilTheSenderFence(t *testing.T) {
	rx, err := transport.NewReceiveWindow(windowScope(), 8<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transport.NewSendWindow(windowScope(), 8<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = rx.Observe(1 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err = rx.ConsumeTo(1 << 20); err != nil {
		t.Fatal(err)
	}
	if err = tx.SentTo(1 << 20); err != nil {
		t.Fatal(err)
	}
	old := rx.Grant()
	if err = tx.ApplyGrant(old); err != nil {
		t.Fatal(err)
	}
	if err = tx.SentTo(3 << 20); err != nil {
		t.Fatal(err)
	} // another 2MiB still in flight
	request, err := rx.BeginRecall(256 << 10)
	if err != nil {
		t.Fatal(err)
	}
	if rx.Snapshot().Reserved != 8<<20 {
		t.Fatal("request alone returned promised memory")
	}
	fence, ok, err := tx.Recall(request)
	if err != nil || !ok {
		t.Fatal(fence, err)
	}
	if tx.Snapshot().Available != 0 {
		t.Fatal("sender continued old credit after fencing", tx.Snapshot())
	}
	freed, err := rx.Fence(fence)
	if err != nil || freed != 6<<20 || rx.Snapshot().Reserved != 2<<20 {
		t.Fatal("in-flight old bytes lost backing", freed, err, rx.Snapshot())
	}
	if err = rx.Observe(3 << 20); err != nil {
		t.Fatal("late old DATA rejected within fence", err)
	}
	freed, err = rx.ConsumeTo(3 << 20)
	if err != nil || freed != (2<<20)-(256<<10) || rx.Snapshot().Reserved != 256<<10 {
		t.Fatal(freed, err, rx.Snapshot())
	}
	if err = tx.ApplyGrant(rx.Grant()); err != nil {
		t.Fatal(err)
	}
	before := tx.Snapshot()
	if err = tx.ApplyGrant(old); err != nil || tx.Snapshot() != before {
		t.Fatal("late old credit reopened the returned window", tx.Snapshot(), err)
	}
	if err = tx.SentTo((3 << 20) + 32); err != nil {
		t.Fatal("short request needed an extra grant round trip", err)
	}
}

func TestNewLeaseDataCanOvertakeFenceAndDuplicateRecallKeepsOriginalWatermark(t *testing.T) {
	rx, err := transport.NewReceiveWindow(windowScope(), 8<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transport.NewSendWindow(windowScope(), 8<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.SentTo(64); err != nil {
		t.Fatal(err)
	}
	if err = rx.Observe(64); err != nil {
		t.Fatal(err)
	}
	if _, err = rx.ConsumeTo(64); err != nil {
		t.Fatal(err)
	}
	request, err := rx.BeginRecall(256 << 10)
	if err != nil {
		t.Fatal(err)
	}
	fence, ok, err := tx.Recall(request)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err = tx.SentTo(64 + (32 << 10)); err != nil {
		t.Fatal("provisional short credit was unavailable", err)
	}
	if err = rx.Observe(64 + (32 << 10)); err != nil {
		t.Fatal(err)
	}
	if freed, err := rx.ConsumeTo(64 + (32 << 10)); err != nil || freed != 0 {
		t.Fatal("memory returned before fence", freed, err)
	}
	if retry, ok := rx.PendingRecall(); !ok || retry != request {
		t.Fatal("control recovery recomputed a different provisional edge", retry)
	}
	duplicate, ok, err := tx.Recall(request)
	if err != nil || !ok || duplicate != fence || duplicate.Sent != 64 {
		t.Fatal("duplicate enlarged old exposure", duplicate, err)
	}
	if freed, err := rx.Fence(fence); err != nil || freed != (8<<20)-(256<<10) {
		t.Fatal("new DATA overtaking the fence broke backing", freed, err)
	}
	if freed, err := rx.Fence(fence); err != nil || freed != 0 {
		t.Fatal("duplicate fence returned memory twice", freed, err)
	}
	if err = rx.Observe(rx.Grant().Limit + 1); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("old oversized flight remained authorized", err)
	}
}

func TestWindowLeaseScopeAndBadWatermarkCannotReleaseBackingOrCreateCredit(t *testing.T) {
	rx, err := transport.NewReceiveWindow(windowScope(), 1<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transport.NewSendWindow(windowScope(), 1<<20, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = rx.Observe(128 << 10); err != nil {
		t.Fatal(err)
	}
	request, err := rx.BeginRecall(64 << 10)
	if err != nil {
		t.Fatal(err)
	}
	before := rx.Snapshot()
	for _, bad := range []transport.WindowFence{
		{Scope: windowScope(), Epoch: request.Epoch, Sent: 0},
		{Scope: windowScope(), Epoch: request.Epoch, Sent: 2 << 20},
		{Scope: windowScope(), Epoch: request.Epoch + 1, Sent: 128 << 10},
	} {
		if _, err = rx.Fence(bad); err == nil || rx.Snapshot() != before {
			t.Fatal("invalid fence changed backing", bad, err)
		}
	}
	for _, change := range []func(*transport.WindowScope){func(s *transport.WindowScope) { s.Stream++ }, func(s *transport.WindowScope) { s.Generation++ }, func(s *transport.WindowScope) { s.Session[0]++ }, func(s *transport.WindowScope) { s.Direction = transport.ServerToClient }} {
		bad := request
		change(&bad.Scope)
		state := tx.Snapshot()
		if _, _, err = tx.Recall(bad); err == nil || tx.Snapshot() != state {
			t.Fatal("foreign scope created a lease", err)
		}
		if _, err = rx.Fence(transport.WindowFence{Scope: bad.Scope, Epoch: request.Epoch, Sent: 128 << 10}); err == nil || rx.Snapshot() != before {
			t.Fatal("foreign fence released backing", err)
		}
	}
	state := tx.Snapshot()
	if err = tx.ApplyGrant(transport.WindowGrant{Scope: windowScope(), Epoch: 2, Limit: 8 << 20}); err == nil || tx.Snapshot() != state {
		t.Fatal("unknown lease granted credit", err)
	}
}

func TestWindowGrowthCannotPromiseUnavailableMemoryOrBypassPendingRecall(t *testing.T) {
	rx, err := transport.NewReceiveWindow(windowScope(), 256<<10, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	before := rx.Snapshot()
	if _, err = rx.Grow(1<<20, 512<<10); !errors.Is(err, transport.ErrCapacity) || rx.Snapshot() != before {
		t.Fatal("growth invented backing", err)
	}
	if added, err := rx.Grow(1<<20, 768<<10); err != nil || added != 768<<10 {
		t.Fatal(added, err)
	}
	if _, err = rx.BeginRecall(256 << 10); err != nil {
		t.Fatal(err)
	}
	before = rx.Snapshot()
	if _, err = rx.Grow(8<<20, 8<<20); !errors.Is(err, transport.ErrCapacity) || rx.Snapshot() != before {
		t.Fatal("growth bypassed unresolved recall", err)
	}
}

func TestConsumptionDemandLeavesSparseTrafficSmallAndGrowsBulkPromptly(t *testing.T) {
	var demand transport.ReceiveDemand
	now := time.Now()
	window := uint64(256 << 10)
	for n := 0; n < 1000; n++ {
		if target := demand.Consume(now.Add(time.Duration(n)*time.Second), 320, window, 8<<20, 64<<10); target != window {
			t.Fatal("light traffic accumulated into bulk reservation", n, target)
		}
	}
	start := now.Add(1100 * time.Second)
	for n := 0; n < 64 && window < 8<<20; n++ {
		window = demand.Consume(start.Add(time.Duration(n)*20*time.Millisecond), 64<<10, window, 8<<20, 64<<10)
	}
	if window != 8<<20 {
		t.Fatal("sustained consumption stayed at its startup window", window)
	}
	if target := demand.Consume(start.Add(5*time.Minute), 32, 256<<10, 8<<20, 64<<10); target != 256<<10 {
		t.Fatal("idle retained a false active-demand burst", target)
	}
}

func TestReorderedWindowControlAlwaysBacksTheSendersEntireRemainingPermission(t *testing.T) {
	type control struct {
		grant  *transport.WindowGrant
		recall *transport.WindowRecall
		fence  *transport.WindowFence
	}
	for seed := int64(1); seed <= 100; seed++ {
		random := rand.New(rand.NewSource(seed))
		rx, err := transport.NewReceiveWindow(windowScope(), 256<<10, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := transport.NewSendWindow(windowScope(), 256<<10, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		var controls []control
		var data []uint64
		var received uint64
		for step := 0; step < 200; step++ {
			switch random.Intn(6) {
			case 0:
				target := uint64(64<<10) << random.Intn(5)
				if _, err := rx.Grow(target, (1<<20)-rx.Snapshot().Reserved); err == nil {
					g := rx.Grant()
					controls = append(controls, control{grant: &g})
				}
			case 1:
				s := tx.Snapshot()
				if s.Available > 0 {
					end := s.Sent + min(s.Available, uint64(1+random.Intn(64<<10)))
					if err := tx.SentTo(end); err != nil {
						t.Fatal(seed, step, err)
					}
					data = append(data, end)
				}
			case 2:
				if len(data) > 0 {
					received = data[0]
					data = data[1:]
					if err := rx.Observe(received); err != nil {
						t.Fatal(seed, step, "late DATA lost backing", err, rx.Snapshot(), tx.Snapshot())
					}
				}
			case 3:
				if _, err := rx.ConsumeTo(received); err != nil {
					t.Fatal(seed, step, err)
				}
				g := rx.Grant()
				controls = append(controls, control{grant: &g})
			case 4:
				if request, err := rx.BeginRecall(uint64(16<<10) << random.Intn(3)); err == nil {
					controls = append(controls, control{recall: &request}, control{recall: &request})
				}
			case 5:
				if len(controls) > 0 {
					index := random.Intn(len(controls))
					event := controls[index]
					controls = append(controls[:index], controls[index+1:]...)
					if event.grant != nil {
						if err := tx.ApplyGrant(*event.grant); err != nil {
							t.Fatal(seed, step, "delayed grant", err)
						}
					}
					if event.recall != nil {
						fence, ok, err := tx.Recall(*event.recall)
						if err != nil {
							t.Fatal(seed, step, "recall", err)
						}
						if ok {
							controls = append(controls, control{fence: &fence}, control{fence: &fence})
						}
					}
					if event.fence != nil {
						if _, err := rx.Fence(*event.fence); err != nil {
							t.Fatal(seed, step, "delayed fence", err, rx.Snapshot(), tx.Snapshot())
						}
					}
				}
			}
			r, s := rx.Snapshot(), tx.Snapshot()
			if s.Limit > r.Limit || s.Sent > s.Limit || r.Reserved > 1<<20 {
				t.Fatal(seed, step, "credit exceeded real receiver backing", r, s)
			}
		}
	}
}
