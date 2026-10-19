package tunnel

import (
	"crypto/sha256"
	"encoding/binary"
	"itom.local/core/wire"
	"sync"
	"sync/atomic"
	"time"
)

// Only repeated evidence-identical advisory state is throttled. Grant usage,
// publication descriptors, liveness and durable receipts are never dropped.
// A fresh payload or a different owner/session (one object per lane) is distinct.
type controlRepeat struct {
	mu         sync.Mutex
	seen       map[[32]byte]time.Time
	suppressed atomic.Uint64
}

func (c *controlRepeat) duplicate(f wire.Frame, now time.Time) bool {
	switch f.Type {
	case wire.Status, wire.Nack, wire.StatusRequest:
	default:
		return false
	}
	raw := binary.BigEndian.AppendUint64(nil, uint64(f.Type))
	raw = binary.BigEndian.AppendUint64(raw, f.Stream)
	raw = binary.BigEndian.AppendUint64(raw, f.Offset)
	raw = binary.BigEndian.AppendUint64(raw, f.Logical)
	raw = append(raw, f.Payload...)
	key := sha256.Sum256(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = make(map[[32]byte]time.Time)
	}
	if at, ok := c.seen[key]; ok && now.Sub(at) >= 0 && now.Sub(at) < 250*time.Millisecond {
		c.suppressed.Add(1)
		return true
	}
	for k, at := range c.seen {
		if now.Sub(at) >= 250*time.Millisecond {
			delete(c.seen, k)
		}
	}
	// Saturation must never suppress unseen state. Do not make map growth depend
	// on an unbounded stream cardinality.
	if len(c.seen) >= 256 {
		return false
	}
	c.seen[key] = now
	return false
}
