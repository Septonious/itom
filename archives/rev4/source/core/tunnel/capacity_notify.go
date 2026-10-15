package tunnel

import "sync"

// A generation broadcast wakes all predicates after a capacity change. A
// one-element token queue wakes only one waiter and can strand usable grants.
// Snapshot before checking the predicate, so a racing release cannot be lost.
type capacityNotification struct {
	mu         sync.Mutex
	generation chan struct{}
}

func (n *capacityNotification) watch() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.generation == nil {
		n.generation = make(chan struct{})
	}
	return n.generation
}
func (n *capacityNotification) signal() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.generation != nil {
		close(n.generation)
	}
	n.generation = make(chan struct{})
}
