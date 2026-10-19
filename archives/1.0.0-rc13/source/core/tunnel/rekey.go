package tunnel

import (
	"errors"
	"itom.local/core/wire"
	"time"
)

// These fields are lane-actor owned. Admission is atomic because physical DATA
// selection occurs in workers. Do not cancel a known-good APPEND for a planned
// rotation: wait for both endpoints' authenticated mailbox receive evidence.
func (l *lane) quiesceForRekey(token uint64) {
	if l.quiescing.Swap(true) {
		return
	}
	l.rekeyDrainToken = token
	l.ready.Store(false)
	if l.stripe != nil {
		l.stripe.signal()
		l.stripe.wakeReaders()
	}
}

func (l *lane) physicalRekeyDrained(now time.Time) bool {
	drained := l.physicalActive.Load() == 0 && len(l.physicalSlots) == 0 &&
		l.controlWriting.Load() == 0 && len(l.urgentQueue) == 0 &&
		len(l.controlQueue) == 0 && len(l.receiptQueue) == 0 && len(l.maintenanceQueue) == 0
	l.mu.Lock()
	for _, out := range l.out {
		if len(out.counters) != 0 {
			drained = false
			break
		}
	}
	l.mu.Unlock()
	if !drained {
		l.rekeyDrainStableAt = time.Time{}
		return false
	}
	if l.rekeyDrainStableAt.IsZero() {
		l.rekeyDrainStableAt = now
		return false
	}
	// Covers the writer's bounded collection window and an already selected
	// reservation entering beginPhysicalWrite. No new DATA is admitted here.
	return now.Sub(l.rekeyDrainStableAt) >= 300*time.Millisecond
}

func (l *lane) advanceRekey(now time.Time) error {
	if l.cipher == nil {
		return nil
	}
	if !l.quiescing.Load() && l.ready.Load() {
		if !l.side && (l.rekeyRequested || l.cipher.NeedsRekey()) && (l.stripe == nil || l.stripe.beginRekey(l)) {
			l.quiesceForRekey(rekeyRequestToken | l.carrierEpoch)
			if err := l.controlSend(wire.Echo{Token: l.rekeyDrainToken}); err != nil {
				return err
			}
		} else if l.side && l.cipher.NeedsRekey() && !now.Before(l.nextRekeyRequest) {
			if err := l.controlSend(wire.Echo{Token: rekeyRequestToken | l.carrierEpoch}); err != nil && errorCode(err) != "CONTROL_QUEUE_LIMIT" {
				return err
			}
			l.nextRekeyRequest = now.Add(5 * time.Second)
		}
	}
	if !l.quiescing.Load() || !l.physicalRekeyDrained(now) {
		return nil
	}
	if l.side {
		if !l.rekeyDrainAckSent {
			if err := l.controlSend(wire.Echo{Token: l.rekeyDrainToken, Reply: true}); err != nil {
				return err
			}
			l.rekeyDrainAckSent = true
		}
	} else if l.rekeyPeerDrained {
		return errors.New("CARRIER_REKEY")
	}
	return nil
}
