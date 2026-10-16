package tunnel

import (
	"sort"
	"time"
)

// Stable rotated ties retain account fairness. Cost includes work already
// admitted by the shared service, physical writer occupancy, peer BODY debt,
// and control debt. Reservation still occurs atomically in the existing grant
// and physical-slot paths; an estimate is never an authorization to publish.
func (g *stripeGroup) prioritizeBulkTargetsLocked(keys []string) {
	if len(keys) == 0 {
		return
	}
	start := int(g.turn % uint64(len(keys)))
	rotated := append(append([]string(nil), keys[start:]...), keys[:start]...)
	copy(keys, rotated)
	ageCost := map[string]int64{}
	now := time.Now()
	target := g.delivery.targetAge()
	for _, r := range g.delivery.ranges {
		if r.carrier == "" || r.fast {
			continue
		}
		if age := now.Sub(r.admitted) - target; age > 0 {
			ageCost[r.carrier] = max(ageCost[r.carrier], min(int64(2000), age.Milliseconds()/4))
		}
	}
	cost := func(l *lane) int64 {
		if l == nil {
			return 1 << 60
		}
		unread := max(uint64(max(int64(0), l.unreceivedLocalBytes.Load())), g.delivery.carrierBytes[l.account.ID])
		n := l.predictedServiceMS(false) + int64(len(l.physicalSlots))*65 + int64(unread/(32<<10)) + int64(l.controlDebt.Load()) + ageCost[l.account.ID] + g.carrierLatencyLocked(l)/4
		if ack := g.delivery.carrierACK[l.account.ID]; ack > 0 {
			if now.Sub(g.delivery.carrierACKAt[l.account.ID]) <= 15*time.Second {
				n += min(int64(1000), ack.Milliseconds()/4)
			}
		}
		if m, ok := l.mail.(interface{ DataQueueCost() int64 }); ok {
			n += m.DataQueueCost()
		}
		return n
	}
	// Snapshot once per selection. Re-reading queue/RTT state inside the sort
	// comparator repeats pool scans and can make comparisons inconsistent.
	scores := make(map[string]int64, len(keys))
	for _, key := range keys {
		scores[key] = cost(g.lanes[key])
	}
	sort.SliceStable(keys, func(i, j int) bool { return scores[keys[i]] < scores[keys[j]] })
}
func (m *observedMail) DataQueueCost() int64 {
	if d, ok := m.Transport.(interface{ DataQueueCost() int64 }); ok {
		return d.DataQueueCost()
	}
	return 0
}
