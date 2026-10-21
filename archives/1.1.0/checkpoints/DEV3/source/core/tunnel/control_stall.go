package tunnel

import "time"

// controlStallPenalty is advisory routing cost, never peer progress, a reason
// to reconnect, or authority to release outstanding physical/logical debt.
func (l *lane) controlStallPenalty(now time.Time) int64 {
	if l == nil || l.controlWriting.Load() == 0 {
		return 0
	}
	started := l.controlWriteStarted.Load()
	if started <= 0 {
		return 0
	}
	age := now.Sub(time.Unix(0, started))
	budget := max(time.Second, min(3*time.Second, 250*time.Millisecond+2*time.Duration(l.idleRTT.Load())*time.Millisecond))
	if age <= budget {
		return 0
	}
	return min(int64(15000), age.Milliseconds())
}
