package transport

import (
	"testing"
)

// Command-lifetime bookkeeping only: no socket, disk, RTT or Mbps claim. The
// original completion mutex and tag lifecycle still execute on every iteration.
func BenchmarkCompletedIMAPCommandLifetime(b *testing.B) {
	e := &Executor{limits: DefaultIMAPLimits(), pending: make(map[string]*operation)}
	b.ReportAllocs()
	for b.Loop() {
		tag, op, err := e.begin(commandNoop, false, false)
		if err != nil {
			b.Fatal(err)
		}
		e.mu.Lock()
		delete(e.pending, tag)
		e.completedLocked(op, false)
		close(op.done)
		e.mu.Unlock()
	}
}
