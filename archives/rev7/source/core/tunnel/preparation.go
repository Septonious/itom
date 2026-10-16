package tunnel

import (
	"context"
	"sync"

	"itom.local/core/datapath"
)

type preparationWaiter struct {
	owner    *lane
	assigned bool
}

// Share preparation across logical owners, rather than letting every owner's
// eight workers independently flood the same physical writers with tiny mail.
// This limits work before sealing, grant Prepare/Spend or uncertain publication.
// Fast DATA's independent worker bypasses it; physical/account grants still
// determine whether a prepared batch can actually APPEND.
func (l *lane) AcquireBulkPreparation(ctx context.Context) (func(), error) {
	if l.stripe == nil {
		return func() {}, nil
	}
	g := l.stripe
	w := &preparationWaiter{owner: l}
	g.mu.Lock()
	if g.preparedByOwner == nil {
		g.preparedByOwner = make(map[*lane]int)
	}
	if len(g.preparation) >= 512 {
		g.mu.Unlock()
		return nil, errDeliveryLimit
	}
	g.preparation = append(g.preparation, w)
	g.mu.Unlock()
	cleanup := func() {
		g.mu.Lock()
		if w.assigned {
			g.prepared--
			g.preparedByOwner[l]--
			if g.preparedByOwner[l] == 0 {
				delete(g.preparedByOwner, l)
			}
			w.assigned = false
		} else {
			for i, p := range g.preparation {
				if p == w {
					g.preparation = append(g.preparation[:i], g.preparation[i+1:]...)
					break
				}
			}
		}
		g.dispatchPreparationLocked()
		g.mu.Unlock()
		g.signal()
	}
	for {
		wake := g.capacityNotice.watch()
		if err := ctx.Err(); err != nil {
			cleanup()
			return nil, err
		}
		g.mu.Lock()
		g.dispatchPreparationLocked()
		assigned := w.assigned
		g.mu.Unlock()
		if assigned {
			var once sync.Once
			return func() { once.Do(cleanup) }, nil
		}
		select {
		case <-ctx.Done():
			cleanup()
			return nil, ctx.Err()
		case <-wake:
		}
	}
}
func (g *stripeGroup) dispatchPreparationLocked() {
	quiet := g.interactiveReadersLocked()
	slots := 0
	for _, l := range g.lanes {
		if l == nil || l == quiet[0] || l == quiet[1] || !l.streamCarrierReady() || l.ctx.Err() != nil {
			continue
		}
		slots += physicalSlotLimit(l)
	}
	owners := map[*lane]bool{}
	for _, l := range g.owners {
		if l != nil && max(l.active.Load(), l.admitted.Load()) > 0 {
			owners[l] = true
		}
	}
	share := min(datapath.MaxBulkWriters, max(1, (slots+max(1, len(owners))-1)/max(1, len(owners))))
	changed := false
	for i := 0; i < len(g.preparation) && g.prepared < slots; {
		w := g.preparation[i]
		if g.preparedByOwner[w.owner] >= share {
			i++
			continue
		}
		g.preparation = append(g.preparation[:i], g.preparation[i+1:]...)
		w.assigned = true
		g.prepared++
		g.preparedByOwner[w.owner]++
		changed = true
	}
	if changed {
		g.signal()
	}
}
