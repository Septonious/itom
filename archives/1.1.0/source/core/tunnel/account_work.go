package tunnel

import (
	"context"
	"errors"
	"sync"
	"time"
)

type accountWorkClass uint8

const (
	workUrgent accountWorkClass = iota
	workReceive
	workMaintenance
	workData
	workDataReceive
	workClassCount
)

var errAccountWorkLimit = errors.New("ACCOUNT_COMMAND_QUEUE_LIMIT")

type accountWorkWaiter struct{ ready chan struct{} }

// One account-wide admission authority covers both DATA and non-DATA I/O.
// Idle capacity is lent; a queued class receives its protected first slot before
// another class receives a second. No in-progress IMAP command is preempted.
type accountWorkBudget struct {
	mu        sync.Mutex
	queues    [workClassCount][]*accountWorkWaiter
	busy      [workClassCount]int
	service   [workClassCount]time.Duration
	completed [workClassCount]uint64
	turn      int
	capacity  int
}

var accountWorkLimits = [workClassCount]int{2, 2, 1, 2, 2}

func (b *accountWorkBudget) acquire(ctx context.Context, class accountWorkClass) (func(), error) {
	if class >= workClassCount {
		return nil, errAccountWorkLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &accountWorkWaiter{ready: make(chan struct{})}
	b.mu.Lock()
	if len(b.queues[class]) >= 32 {
		b.mu.Unlock()
		return nil, errAccountWorkLimit
	}
	b.queues[class] = append(b.queues[class], w)
	b.dispatch(class)
	b.mu.Unlock()
	select {
	case <-w.ready:
		started := time.Now()
		var once sync.Once
		return func() {
			once.Do(func() {
				b.mu.Lock()
				b.busy[class]--
				elapsed := time.Since(started)
				if b.service[class] == 0 {
					b.service[class] = elapsed
				} else {
					b.service[class] = (7*b.service[class] + elapsed) / 8
				}
				b.completed[class]++
				b.dispatch(class)
				b.mu.Unlock()
			})
		}, nil
	case <-ctx.Done():
		b.mu.Lock()
		removed := false
		for i, p := range b.queues[class] {
			if p == w {
				b.queues[class] = append(b.queues[class][:i], b.queues[class][i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			b.busy[class]--
		}
		b.dispatch(class)
		b.mu.Unlock()
		return nil, ctx.Err()
	}
}
func (b *accountWorkBudget) dispatch(_ accountWorkClass) {
	limit := b.capacity
	if limit <= 0 {
		limit = 7
	}
	total := 0
	for _, n := range b.busy {
		total += n
	}
	for total < limit {
		chosen := -1
		for pass := 0; pass < 2 && chosen < 0; pass++ {
			for off := 0; off < int(workClassCount); off++ {
				i := (b.turn + off) % int(workClassCount)
				if len(b.queues[i]) == 0 || b.busy[i] >= accountWorkLimits[i] {
					continue
				}
				if pass == 0 && b.busy[i] > 0 {
					continue
				}
				chosen = i
				break
			}
		}
		if chosen < 0 {
			return
		}
		w := b.queues[chosen][0]
		b.queues[chosen] = b.queues[chosen][1:]
		b.busy[chosen]++
		total++
		b.turn = (chosen + 1) % int(workClassCount)
		close(w.ready)
	}
}
func (b *accountWorkBudget) dataCost() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	service := b.service[workData]
	if service <= 0 {
		service = 65 * time.Millisecond
	}
	return int64(b.busy[workData]+len(b.queues[workData])) * max(int64(1), service.Milliseconds())
}
func (m *laneMail) command(ctx context.Context, class accountWorkClass, fn func() error) error {
	if m.dialGate == nil {
		return fn()
	}
	release, err := m.dialGate.work.acquire(ctx, class)
	if err != nil {
		return err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func (m *laneMail) DataQueueCost() int64 {
	if m.dialGate == nil {
		return 0
	}
	return m.dialGate.work.dataCost()
}

// Client's internal mutex serializes bytes, but it cannot fairly schedule DATA,
// control and multi-command cleanup that have already passed account admission.
// Admit one turn on the actual shared socket before entering that mutex. The
// existing round-robin class scheduler preserves both urgency and GC/bulk progress;
// independent DATA workers/cleaners retain their original parallel service.
func (m *laneMail) controlCommand(ctx context.Context, class accountWorkClass, fn func() error) error {
	return m.command(ctx, class, func() error { return m.sharedSocketCommand(ctx, class, fn) })
}

// Live reads have independent account admission, but keep their historical
// maintenance turn on the actual shared socket. Giving them a fourth socket
// class would reduce DATA's service share even when every command succeeds.
func (m *laneMail) controlReceiveCommand(ctx context.Context, fn func() error) error {
	return m.command(ctx, workReceive, func() error { return m.sharedSocketCommand(ctx, workMaintenance, fn) })
}

func (m *laneMail) sharedSocketCommand(ctx context.Context, class accountWorkClass, fn func() error) error {
	m.baseWork.mu.Lock()
	m.baseWork.capacity = 1
	m.baseWork.mu.Unlock()
	release, err := m.baseWork.acquire(ctx, class)
	if err != nil {
		return err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return err
	}
	return fn()
}
func (m *laneMail) maintenanceCommand(ctx context.Context, fn func() error) error {
	if m.cleanupClient() == m.controlClient() {
		return m.controlCommand(ctx, workMaintenance, fn)
	}
	return m.command(ctx, workMaintenance, fn)
}
