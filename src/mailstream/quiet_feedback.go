package mailstream

import "time"

// Only the ordinary actor may choose its DATA writer. During desktop quiet
// traffic it is idle, and can deliver receipts without queueing behind fresh
// application control messages on the companion. Never borrow it from a bulk
// batch, a grant transition, recovery work, or the mobile feedback path.
// Called under a.mu by the ordinary actor; inFlight belongs to that actor.
func (a *mailAccountState) quietFeedbackOnData(work mailWork, writer *mailPipe, inFlight int) bool {
	if !work.feedback || work.recovery || writer == nil || a.controlWriter == nil || writer != a.writer || a.id == (SessionID{}) || a.parking.Load() || a.reclaiming.Load() || a.scaleBlocked.Load() || a.writerRecovering || a.link.mobileFeedback.Load() || inFlight != 0 || len(a.bulk) != 0 {
		return false
	}
	last := a.link.lastBulk.Load()
	return last == 0 || time.Since(time.Unix(0, last)) >= 300*time.Millisecond
}
