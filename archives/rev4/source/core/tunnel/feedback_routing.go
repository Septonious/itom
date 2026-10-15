package tunnel

import (
	"itom.local/core/wire"
	"sort"
)

// DATA on independent sockets is not control queue debt. Keep bulk snapshots
// on responsive services so receipt and credit updates coalesce before sealing.
// No account has a permanent role: detached/full paths fall back immediately.
func controlCarrierScore(l *lane) int {
	n := len(l.urgentQueue)*80 + len(l.controlQueue)*15 + len(l.maintenanceQueue)*5 + int(l.controlDebt.Load())*4
	if l.controlWriting.Load() > 0 {
		n += 64
	}
	if !l.independentDataMaintenance() {
		n += int(l.unreceivedLocalRecords.Load())*16 + len(l.physicalSlots)*128
	}
	return n
}

// Rendezvous mixing uses the whole stream ID. Paired upload/download IDs
// (1,5,9 / 3,7,11) must not select only half the available control services.
func feedbackRouteHash(stream uint64, account string) uint64 {
	x := uint64(14695981039346656037)
	for i := 0; i < len(account); i++ {
		x ^= uint64(account[i])
		x *= 1099511628211
	}
	x ^= stream + 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
func (g *stripeGroup) orderBulkFeedbackLocked(candidates []*lane, f wire.Frame) []*lane {
	if len(candidates) == 0 {
		return candidates
	}
	eligible := func(l *lane) bool {
		return l.controlDebt.Load() < 64 && len(l.urgentQueue) < 16 && len(l.controlQueue) < 32
	}
	best := int64(1 << 62)
	for _, l := range candidates {
		if eligible(l) {
			best = min(best, g.carrierLatencyLocked(l))
		}
	}
	ceiling := best
	if best < 1<<62 {
		ceiling = best + max(int64(250), best/2)
	}
	var primary []*lane
	for _, l := range candidates {
		if eligible(l) && g.carrierLatencyLocked(l) <= ceiling {
			primary = append(primary, l)
		}
	}
	if len(primary) == 0 {
		return candidates
	}
	sort.SliceStable(primary, func(i, j int) bool { return primary[i].account.ID < primary[j].account.ID })
	g.feedbackPool = g.feedbackPool[:0]
	for _, l := range primary {
		g.feedbackPool = append(g.feedbackPool, l.account.ID)
	}
	preferred := g.feedbackAffinity[f.Stream]
	chosen := primary[0]
	for _, l := range primary {
		if feedbackRouteHash(f.Stream, l.account.ID) > feedbackRouteHash(f.Stream, chosen.account.ID) {
			chosen = l
		}
	}
	for _, l := range primary {
		if l.account.ID == preferred {
			chosen = l
			break
		}
	}
	ordered := []*lane{chosen}
	used := map[*lane]bool{chosen: true}
	for _, l := range primary {
		if !used[l] {
			ordered = append(ordered, l)
			used[l] = true
		}
	}
	for _, l := range candidates {
		if !used[l] {
			ordered = append(ordered, l)
		}
	}
	return ordered
}
