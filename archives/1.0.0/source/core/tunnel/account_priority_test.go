package tunnel

import (
	"context"
	"sync"
	"testing"
)

func TestAccountPriorityServesShortDataAndCannotStarveBulk(t *testing.T) {
	r := &accountRuntime{ctx: context.Background(), users: []string{"user"}, queues: map[string][]accountAppend{"user": {{user: "user", raw: make([]byte, 256<<10)}}}, fastQueues: map[string][]accountAppend{}, deficit: map[string]int{}}
	r.cond = sync.NewCond(&r.mu)
	for i := 0; i < 16; i++ {
		r.fastQueues["user"] = append(r.fastQueues["user"], accountAppend{user: "user", raw: make([]byte, 8<<10), fast: true})
	}
	bulk := false
	for i := 0; i < 5; i++ {
		req, ok := r.take()
		if !ok {
			t.Fatal("scheduler stopped")
		}
		if i == 0 && !req.fast {
			t.Fatal("short DATA queued behind bulk")
		}
		bulk = bulk || !req.fast
	}
	if !bulk {
		t.Fatal("continuous short DATA starved bulk")
	}
}
