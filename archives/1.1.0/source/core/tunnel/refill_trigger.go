package tunnel

import "time"

// A fenced lease is not physical free space. If the normal offer pass cannot
// restore usable inbound bulk permission, request the existing bounded proof
// service rather than waiting out a periodic timer. No IO/proof/quota is skipped.
// Local-pressure arbitration, queued FIFO admission and gcBusy remain unchanged.
func (l *lane) requestRefillCleanup(now time.Time) {
	if !l.side || l.stripe == nil || !l.stripe.delivery.modelEnabled || l.data == nil || l.cipher == nil {
		return
	}
	if l.remoteCleanupPeriod() >= runtimeRemoteCleanupInterval {
		return
	}
	_, bulkActive := l.issuedGrantCounts()
	if !(bulkActive == 0) {
		return
	}
	if l.control != nil {
		held, err := l.control.Held(l.account.ID, l.user.ID)
		// A control-starved offer needs local control proof, not extra remote work.
		if err != nil || held.Messages >= 64 {
			return
		}
	}
	if l.nextRemoteCleanup.After(now) {
		l.nextRemoteCleanup = now
	}
	l.refillCleanupRequested = true
	select {
	case l.cleanupWake <- struct{}{}:
	default:
	}
}
