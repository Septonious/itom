package tunnel

import (
	"context"
	"sync"
)

type repairAdmissionKey struct{}

func repairAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, repairAdmissionKey{}, true)
}
func priorityLogin(ctx context.Context) bool {
	v, _ := ctx.Value(repairAdmissionKey{}).(bool)
	return v
}

type loginWaiter struct{ ready chan struct{} }
type loginAdmission struct {
	mu                    sync.Mutex
	active, limit, streak int
	urgent, ordinary      []*loginWaiter
}

func (b *loginAdmission) dispatch() {
	for b.active < b.limit {
		q := &b.ordinary
		if len(b.urgent) > 0 && (b.streak < 4 || len(b.ordinary) == 0) {
			q = &b.urgent
			b.streak++
		} else {
			b.streak = 0
		}
		if len(*q) == 0 {
			return
		}
		w := (*q)[0]
		*q = (*q)[1:]
		b.active++
		close(w.ready)
	}
}
func (b *loginAdmission) acquire(ctx context.Context, urgent bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &loginWaiter{ready: make(chan struct{})}
	b.mu.Lock()
	if urgent {
		b.urgent = append(b.urgent, w)
	} else {
		b.ordinary = append(b.ordinary, w)
	}
	b.dispatch()
	b.mu.Unlock()
	select {
	case <-w.ready:
		var once sync.Once
		return func() { once.Do(func() { b.mu.Lock(); b.active--; b.dispatch(); b.mu.Unlock() }) }, nil
	case <-ctx.Done():
		b.mu.Lock()
		removed := false
		for _, q := range []*[]*loginWaiter{&b.urgent, &b.ordinary} {
			for i, x := range *q {
				if x == w {
					*q = append((*q)[:i], (*q)[i+1:]...)
					removed = true
					break
				}
			}
		}
		if !removed {
			b.active--
		}
		b.dispatch()
		b.mu.Unlock()
		return nil, ctx.Err()
	}
}
