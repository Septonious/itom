package tunnel

import (
	"context"
	"itom.local/core/datapath"
	"itom.local/core/flow"
	"itom.local/core/wire"
	"sync"
	"testing"
	"time"
)

// Fast and bulk workers share waiter planning. A completed reservation must
// own its diagnostic snapshot while other workers re-plan the pending queue.
// Run with -race; payload progress verifies that snapshot ownership cannot
// change admission, refund bytes, or leave a waiter behind.
func TestDEV7ConcurrentAdmissionOwnsPlanSnapshot(t *testing.T) {
	g := newModelStripeGroup(wire.ID{1}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const workers = 64
	jobs := make([][]*datapath.Job, workers)
	for i := range jobs {
		size := 32 << 10
		if i%2 == 0 {
			size = 512
		}
		jobs[i] = deliveryJobs(t, uint64(2*i+1), 1, size)
	}
	start := make(chan struct{})
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for _, batch := range jobs {
		wg.Add(1)
		go func(batch []*datapath.Job) {
			defer wg.Done()
			<-start
			r, err := g.reserveDelivery(ctx, batch)
			if err != nil {
				failures <- err
				return
			}
			snapshot := r.debug
			r.finish(r.jobs, committed(r.jobs, flow.CommitUnknown))
			for _, j := range r.jobs {
				a := j.Attempt()
				g.deliveryProgress(a.Stream, a.Offset+a.PayloadBytes)
			}
			if snapshot != r.debug {
				failures <- errDeliveryLimit
			}
		}(batch)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.delivery.waiters) != 0 || g.delivery.bulk != 0 || g.delivery.fast != 0 {
		t.Fatal("admission left queued waiters or charged flight", len(g.delivery.waiters), g.delivery.bulk, g.delivery.fast)
	}
}
