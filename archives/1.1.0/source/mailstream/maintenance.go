package mailstream

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// MaintenancePool owns exactly one server-side maintenance connection per
// physical account, shared by configured identities. No delivery reader or
// APPEND writer runs its commands. An unknown command outcome pauses this pool
// for its lifetime: repeatedly opening sockets after a hung provider command
// could exhaust the very connection allowance reserved for this owner.
type MaintenancePool struct {
	ctx               context.Context
	cancel            context.CancelFunc
	account           MailAccount
	jobs              chan maintenanceJob
	done              chan struct{}
	once              sync.Once
	connected, paused atomic.Bool
	completed         atomic.Uint64
}
type maintenanceJob struct {
	ctx    context.Context
	run    func(*mailPipe) error
	failed func(error)
}
type MaintenanceSnapshot struct {
	Connected bool   `json:"connected"`
	Paused    bool   `json:"paused"`
	Completed uint64 `json:"completed_commands"`
}

func NewMaintenancePool(parent context.Context, account MailAccount) *MaintenancePool {
	ctx, cancel := context.WithCancel(parent)
	p := &MaintenancePool{ctx: ctx, cancel: cancel, account: account, jobs: make(chan maintenanceJob, 32), done: make(chan struct{})}
	go p.run()
	return p
}
func (p *MaintenancePool) Close() { p.once.Do(func() { p.cancel(); <-p.done }) }
func (p *MaintenancePool) Snapshot() MaintenanceSnapshot {
	return MaintenanceSnapshot{p.connected.Load(), p.paused.Load(), p.completed.Load()}
}
func (p *MaintenancePool) submit(job maintenanceJob) error {
	if p.paused.Load() || p.ctx.Err() != nil {
		return ErrClosed
	}
	select {
	case p.jobs <- job:
		return nil
	default:
		return ErrCapacity
	}
}
func (p *MaintenancePool) run() {
	defer close(p.done)
	var pipe *mailPipe
	var closeConnection context.CancelFunc
	defer func() {
		if closeConnection != nil {
			closeConnection()
		}
		if pipe != nil {
			pipe.close()
		}
		p.connected.Store(false)
	}()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		var job maintenanceJob
		select {
		case <-p.ctx.Done():
			return
		case job = <-p.jobs:
			if job.ctx.Err() != nil {
				continue
			}
		case <-tick.C:
			if pipe == nil || p.paused.Load() {
				continue
			}
			job = maintenanceJob{ctx: p.ctx, run: func(m *mailPipe) error { return m.client.Noop().Wait() }}
		}
		if p.paused.Load() {
			if job.failed != nil {
				job.failed(ErrClosed)
			}
			continue
		}
		if pipe == nil {
			var life context.Context
			life, closeConnection = context.WithCancel(p.ctx)
			timer := time.AfterFunc(2*time.Second, closeConnection)
			var err error
			pipe, err = openMailPipe(life, p.account)
			stopped := timer.Stop()
			if err != nil || !stopped {
				p.paused.Store(true)
				closeConnection()
				if pipe != nil {
					pipe.close()
				}
				if job.failed != nil {
					job.failed(ErrClosed)
				}
				continue
			}
			p.connected.Store(true)
		}
		timer := time.AfterFunc(2*time.Second, closeConnection)
		err := job.run(pipe)
		stopped := timer.Stop()
		if err != nil || !stopped {
			p.paused.Store(true)
			closeConnection()
			pipe.close()
			p.connected.Store(false)
			if job.failed != nil {
				job.failed(ErrClosed)
			}
		} else {
			p.completed.Add(1)
		}
	}
}
