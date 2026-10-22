package datapath

import (
	"context"
	"itom.local/core/flow"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTinySetupPriorityStillServesTLSAndBulk(t *testing.T) {
	p := &Pump{ctx: context.Background(), dataTiny: make(chan *Job, 16), dataFast: make(chan *Job, 16), dataBulk: make(chan *Job, 1)}
	for i := 0; i < 8; i++ {
		p.dataTiny <- &Job{attempt: flow.Attempt{ID: uint64(i + 1), PayloadBytes: 80}}
	}
	p.dataFast <- &Job{attempt: flow.Attempt{ID: 20, PayloadBytes: 4096}}
	streak := 0
	for i := 0; i < dataTinyBurst; i++ {
		j, ok := p.nextDataJob(&streak)
		if !ok || j.attempt.PayloadBytes != 80 {
			t.Fatal("setup exchange queued behind TLS", i)
		}
	}
	j, ok := p.nextDataJob(&streak)
	if !ok || j.attempt.ID != 20 {
		t.Fatal("tiny exchanges starved TLS")
	}
	p.dataBulk <- &Job{attempt: flow.Attempt{ID: 30, PayloadBytes: 32768}, bulk: true}
	streak = dataFastBurst
	j, ok = p.nextDataJob(&streak)
	if !ok || j.attempt.ID != 30 {
		t.Fatal("short traffic starved bulk")
	}
}

func TestConcurrentTinyCollectorsOwnEveryJobExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := &Pump{ctx: ctx, dataTiny: make(chan *Job, 64), dataFast: make(chan *Job, 64)}
	for i := 1; i <= 64; i++ {
		j := &Job{attempt: flow.Attempt{ID: uint64(i), PayloadBytes: 80}, queuedAt: time.Now()}
		if i%4 == 0 {
			j.attempt.PayloadBytes = 4096
			p.dataFast <- j
		} else {
			p.dataTiny <- j
		}
	}
	var wg sync.WaitGroup
	var count atomic.Int64
	var mu sync.Mutex
	seen := map[uint64]bool{}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				jobs, ok := p.collectFastBatch()
				if !ok {
					return
				}
				for _, j := range jobs {
					mu.Lock()
					if seen[j.attempt.ID] {
						t.Error("duplicate job", j.attempt.ID)
					}
					seen[j.attempt.ID] = true
					mu.Unlock()
					if count.Add(1) == 64 {
						cancel()
					}
				}
			}
		}()
	}
	wg.Wait()
	if count.Load() != 64 || len(seen) != 64 || len(p.dataTiny)+len(p.dataFast) != 0 {
		t.Fatal("lost setup/TLS jobs", count.Load(), len(seen))
	}
}
