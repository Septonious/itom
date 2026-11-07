package mailbox

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrBusy = errors.New("IMAP_QUEUE_FULL")
var ErrStopped = errors.New("IMAP_DISPATCHER_STOPPED")

// Transport operations must honor cancellation. Client implements this contract.
// One Dispatcher exclusively owns one authenticated account connection. It does
// not govern other processes or other endpoints using the same mail account.
type Transport interface {
	Ensure(context.Context, string) error
	Append(context.Context, string, []byte) error
	Page(context.Context, string, Cursor) ([]Message, Cursor, error)
	Delete(context.Context, Message) error
	Absent(context.Context, string, uint32, uint32) (bool, error)
}
type RouteConfig struct {
	User    string
	Folders []string
}
type DispatchConfig struct {
	Routes       []RouteConfig
	QueuePerUser int
	// Minimum spacing between operation starts. A Page is at most 16 UIDs;
	// spacing is not a count of individual IMAP protocol commands.
	MinInterval time.Duration
}
type request struct {
	ctx  context.Context
	run  func(context.Context) error
	done chan error
}
type routeQueue struct {
	folders map[string]bool
	pending []*request
}
type Dispatcher struct {
	mu             sync.Mutex
	transport      Transport
	ctx            context.Context
	cancel         context.CancelFunc
	wake           chan struct{}
	done           chan struct{}
	queues         []routeQueue
	users          map[string]int
	next, capacity int
	interval       time.Duration
	stopped        bool
}
type Route struct {
	dispatcher *Dispatcher
	index      int
}

func NewDispatcher(parent context.Context, transport Transport, cfg DispatchConfig) (*Dispatcher, error) {
	if transport == nil || len(cfg.Routes) == 0 || len(cfg.Routes) > 128 || cfg.QueuePerUser < 1 || cfg.QueuePerUser > 32 || cfg.MinInterval < 0 || cfg.MinInterval > time.Minute {
		return nil, ErrScope
	}
	d := &Dispatcher{transport: transport, wake: make(chan struct{}, 1), done: make(chan struct{}), users: map[string]int{}, capacity: cfg.QueuePerUser, interval: cfg.MinInterval}
	all := map[string]bool{}
	for _, r := range cfg.Routes {
		if r.User == "" || len(r.User) > 64 || len(r.Folders) == 0 || len(r.Folders) > 4 {
			return nil, ErrScope
		}
		if _, ok := d.users[r.User]; ok {
			return nil, ErrScope
		}
		q := routeQueue{folders: map[string]bool{}}
		for _, f := range r.Folders {
			if !validFolder(f) || all[f] {
				return nil, ErrScope
			}
			all[f] = true
			q.folders[f] = true
		}
		d.users[r.User] = len(d.queues)
		d.queues = append(d.queues, q)
	}
	d.ctx, d.cancel = context.WithCancel(parent)
	go d.loop()
	return d, nil
}
func (d *Dispatcher) Route(user string) (*Route, error) {
	index, ok := d.users[user]
	if !ok {
		return nil, ErrScope
	}
	return &Route{d, index}, nil
}

// Close cancels queued and running work and joins the worker. The caller closes
// its connection only afterwards. No reconnect or implicit APPEND retry occurs.
func (d *Dispatcher) Close() { d.cancel(); <-d.done }
func (r *Route) submit(ctx context.Context, folder string, fn func(context.Context) error) error {
	d := r.dispatcher
	if !d.queues[r.index].folders[folder] {
		return ErrScope
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req := &request{ctx: ctx, run: fn, done: make(chan error, 1)}
	d.mu.Lock()
	if d.stopped || d.ctx.Err() != nil {
		d.mu.Unlock()
		return ErrStopped
	}
	q := &d.queues[r.index]
	if len(q.pending) >= d.capacity {
		d.mu.Unlock()
		return ErrBusy
	}
	q.pending = append(q.pending, req)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default:
	}
	// A running APPEND must report its real completion, including unknown outcome.
	// Joining avoids an early context error concealing an operation still in flight.
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		d.mu.Lock()
		q := &d.queues[r.index]
		for i, p := range q.pending {
			if p == req {
				copy(q.pending[i:], q.pending[i+1:])
				q.pending[len(q.pending)-1] = nil
				q.pending = q.pending[:len(q.pending)-1]
				d.mu.Unlock()
				return ctx.Err()
			}
		}
		d.mu.Unlock()
		return <-req.done
	}
}
func (d *Dispatcher) take() *request {
	d.mu.Lock()
	defer d.mu.Unlock()
	for offset := 0; offset < len(d.queues); offset++ {
		i := (d.next + offset) % len(d.queues)
		q := &d.queues[i]
		if len(q.pending) == 0 {
			continue
		}
		r := q.pending[0]
		q.pending[0] = nil
		q.pending = q.pending[1:]
		d.next = (i + 1) % len(d.queues)
		return r
	}
	return nil
}
func (d *Dispatcher) loop() {
	defer close(d.done)
	defer func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.stopped = true
		for i := range d.queues {
			for _, r := range d.queues[i].pending {
				r.done <- ErrStopped
			}
			d.queues[i].pending = nil
		}
	}()
	var last time.Time
	for {
		if d.ctx.Err() != nil {
			return
		}
		r := d.take()
		if r == nil {
			select {
			case <-d.ctx.Done():
				return
			case <-d.wake:
				continue
			}
		}
		ctx, cancel := context.WithCancel(r.ctx)
		stop := context.AfterFunc(d.ctx, cancel)
		if d.ctx.Err() != nil {
			cancel()
		}
		if delay := time.Until(last.Add(d.interval)); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
			case <-timer.C:
			}
			timer.Stop()
		}
		err := ctx.Err()
		if err == nil {
			last = time.Now()
			err = r.run(ctx)
		}
		stop()
		cancel()
		r.done <- err
	}
}
func (r *Route) Ensure(ctx context.Context, f string) error {
	return r.submit(ctx, f, func(c context.Context) error { return r.dispatcher.transport.Ensure(c, f) })
}
func (r *Route) Append(ctx context.Context, f string, b []byte) error {
	if len(b) == 0 || len(b) > MaxMIME {
		return ErrLimit
	}
	// Synchronous ownership: the caller must not mutate b until this call returns.
	return r.submit(ctx, f, func(c context.Context) error { return r.dispatcher.transport.Append(c, f, b) })
}
func (r *Route) Page(ctx context.Context, f string, cur Cursor) (out []Message, next Cursor, err error) {
	next = cur
	err = r.submit(ctx, f, func(c context.Context) error {
		var e error
		out, next, e = r.dispatcher.transport.Page(c, f, cur)
		return e
	})
	return
}
func (r *Route) Delete(ctx context.Context, m Message) error {
	return r.submit(ctx, m.Folder, func(c context.Context) error { return r.dispatcher.transport.Delete(c, m) })
}
func (r *Route) Absent(ctx context.Context, f string, v, u uint32) (absent bool, err error) {
	err = r.submit(ctx, f, func(c context.Context) error {
		var e error
		absent, e = r.dispatcher.transport.Absent(c, f, v, u)
		return e
	})
	return
}
