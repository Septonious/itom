package tunnel

// cleanupQueue is FIFO among requesting physical carriers. An admitted cleaner
// may drain in-flight fast work, but queued cleaners leave their lane available.
// Reserve two fast carriers with at least five READY accounts, otherwise one.
// A single spare can coincide with a lease rollover and delay both duplex
// legs behind maintenance. Limits change service scheduling only, never debt.
func cleanupFastSpares(ready int) int {
	if ready <= 1 {
		return 0
	}
	if ready >= 5 {
		return 2
	}
	return 1
}

func (l *lane) cleanupFastReady() bool {
	if l.controlDebt.Load() >= 80 {
		return false
	}
	// S2C uses the reserved local byte/message headroom. C2S must have a
	// received permit, or already be using one under the physical fast guard.
	// Bulk permits cannot carry short DATA or interactive controls. Counting
	// them here can admit cleanup on every genuinely fast-ready carrier.
	return l.side || len(l.fastGrants) > 0 || l.maintenanceIO.Load() > 0
}

func (g *stripeGroup) beginCleanup(l *lane) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cleanupActive[l] {
		return true
	}
	found := false
	for _, waiting := range g.cleanupQueue {
		if waiting == l {
			found = true
			break
		}
	}
	if !found {
		g.cleanupQueue = append(g.cleanupQueue, l)
	}
	chosen := g.cleanupChoiceLocked()
	if chosen < 0 {
		return false
	}
	if g.cleanupQueue[chosen] != l {
		g.wakeCleanupLocked(chosen)
		return false
	}
	copy(g.cleanupQueue[chosen:], g.cleanupQueue[chosen+1:])
	g.cleanupQueue[len(g.cleanupQueue)-1] = nil
	g.cleanupQueue = g.cleanupQueue[:len(g.cleanupQueue)-1]
	g.cleanupActive[l] = true
	g.wakeCleanupLocked(g.cleanupChoiceLocked())
	return true
}

// cleanupChoiceLocked returns the oldest admissible waiter. The actor owning
// that waiter must receive the released turn directly: a 100 ms timer per FIFO
// handoff serializes short server cleanups and leaves physical credit idle.
func (g *stripeGroup) cleanupChoiceLocked() int {
	ready, healthyOutside := 0, 0
	for _, carrier := range g.lanes {
		if carrier != nil && carrier.streamCarrierReady() && carrier.ctx.Err() == nil {
			ready++
			if !g.cleanupActive[carrier] && carrier.cleanupFastReady() {
				healthyOutside++
			}
		}
	}
	spares := cleanupFastSpares(ready)
	limit := max(1, ready-spares)
	for i := 0; i < len(g.cleanupQueue); i++ {
		waiting := g.cleanupQueue[i]
		if waiting.ctx.Err() != nil || g.lanes[waiting.account.ID] != waiting {
			copy(g.cleanupQueue[i:], g.cleanupQueue[i+1:])
			g.cleanupQueue[len(g.cleanupQueue)-1] = nil
			g.cleanupQueue = g.cleanupQueue[:len(g.cleanupQueue)-1]
			i--
		}
	}
	if len(g.cleanupActive) >= limit {
		return -1
	}
	// Preserve fast carriers whose descriptor/control path has headroom. A
	// debt-blocked cleaner may pass a temporarily inadmissible healthy waiter
	// so draining that debt can create another usable carrier.
	quiet := g.interactiveReadersLocked()
	chosen := -1
	for i, waiting := range g.cleanupQueue {
		if waiting.cleanupFastReady() && (waiting == quiet[0] || waiting == quiet[1]) {
			other := quiet[0]
			if waiting == other {
				other = quiet[1]
			}
			if other != nil && g.cleanupActive[other] {
				continue
			}
		}
		if waiting.cleanupFastReady() && healthyOutside <= spares {
			continue
		}
		chosen = i
		break
	}
	return chosen
}

func (g *stripeGroup) wakeCleanupLocked(chosen int) {
	if chosen < 0 {
		return
	}
	select {
	case g.cleanupQueue[chosen].cleanupWake <- struct{}{}:
	default:
	}
}

func (g *stripeGroup) removeCleanupLocked(l *lane) {
	delete(g.cleanupActive, l)
	for i := 0; i < len(g.cleanupQueue); i++ {
		if g.cleanupQueue[i] == l {
			copy(g.cleanupQueue[i:], g.cleanupQueue[i+1:])
			g.cleanupQueue[len(g.cleanupQueue)-1] = nil
			g.cleanupQueue = g.cleanupQueue[:len(g.cleanupQueue)-1]
			i--
		}
	}
}
func (g *stripeGroup) endCleanup(l *lane) {
	g.mu.Lock()
	g.removeCleanupLocked(l)
	g.wakeCleanupLocked(g.cleanupChoiceLocked())
	g.mu.Unlock()
	g.signal()
}
