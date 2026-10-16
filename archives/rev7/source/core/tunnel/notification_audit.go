package tunnel

import "time"

// A timeout followed by actual rows is evidence that IDLE failed to notify
// before the audit. Two observations enable a bounded active-traffic fallback;
// quiet accounts keep their original cadence. No clock here is peer progress,
// physical quota evidence or permission to advance a UID cursor.
type notificationAuditPolicy struct {
	awaitingAudit bool
	misses        int
	fastUntil     time.Time
}

func (p *notificationAuditPolicy) fetched(rows int, now time.Time) {
	if p.awaitingAudit && rows > 0 {
		p.misses++
		if p.misses >= 2 {
			p.fastUntil = now.Add(30 * time.Second)
			p.misses = 0
		}
	}
	p.awaitingAudit = false
}
func (p *notificationAuditPolicy) waited(notified, kicked bool) {
	p.awaitingAudit = !notified && !kicked
}
func (p *notificationAuditPolicy) delay(active int64, base time.Duration, now time.Time) time.Duration {
	if active > 0 && now.Before(p.fastUntil) {
		return min(base, 150*time.Millisecond)
	}
	return base
}
