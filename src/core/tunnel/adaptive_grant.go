package tunnel

import (
	"itom.local/core/accountpolicy"
	"itom.local/mailstream"
	"slices"
	"time"
)

// A grant is a ceiling, not a promise of bandwidth. The client independently
// controls active paths. Reserve all baseline paths; lend one extra path only
// after measured sustained demand and health. All changes use LiveAccountGrant.
const elasticGrantCooldown = 10 * time.Second

type grantDemandWatch struct {
	at, lastChange      time.Time
	healthy, quiet, bad int
	demand              bool
	bytes, retries      uint64
}

func (a *sessionAdmission) observeDemand(id string, mail mailstream.MailSnapshot, s mailstream.Snapshot, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.accounts)
	grant := a.grants[id]
	if n <= accountpolicy.SharedProfileMaximum || a.owners[id] == 0 || !mail.GrantNegotiated || mail.GrantChanging || mail.AllocatedAccounts != len(grant) || s.Closed {
		return false
	}
	index := slices.Index(a.order, id)
	if index < 0 {
		return false
	}
	baseline := accountpolicy.BaselineCount(n, len(a.order), index)
	if a.adaptive == nil {
		a.adaptive = map[string]*grantDemandWatch{}
	}
	w := a.adaptive[id]
	if w == nil {
		w = &grantDemandWatch{}
		a.adaptive[id] = w
	}
	total := s.BulkDelivered + s.ReceivedPayload
	active, fresh := 0, 0
	badPath := false
	byID := map[string]mailstream.MailAccountSnapshot{}
	for _, l := range mail.Lines {
		byID[l.Account] = l
		if l.Allocated != slices.Contains(grant, l.Account) {
			return false
		}
		if l.Allocated && l.PeerActive && l.Ready && l.Data && l.Writer && l.ControlWriter && l.Failure == "" && !l.PeerUnavailable {
			active++
			if l.IMAPLatencyFresh && l.IMAPLatencyMillis > 0 {
				fresh++
				if l.IMAPLatencyMillis >= 250 {
					badPath = true
				}
			}
			if l.BulkBlocked || l.ControlBackpressure {
				badPath = true
			}
		}
	}
	contiguous := !w.at.IsZero() && now.Sub(w.at) > 0 && now.Sub(w.at) <= 5*time.Second && total >= w.bytes && s.Retransmissions >= w.retries
	progressing := contiguous && total > w.bytes
	retries := contiguous && s.Retransmissions > w.retries
	w.at, w.bytes, w.retries = now, total, s.Retransmissions
	pending := s.BulkOutstanding >= 128<<10 || s.PeerDemandFresh && s.PeerBulkPending >= 128<<10
	w.demand = pending || progressing
	// The peer's receipt RTT reflects the end-to-end mail path; a fast server
	// IMAP command by itself is not proof of healthy cellular bandwidth.
	controlHealthy := s.InteractiveReceiptRTTMS > 0 && s.InteractiveReceiptRTTMS <= max(450, 2*s.InteractiveReceiptMinRTTMS) && s.InteractiveReceiptRTTMS < 700
	pressure := s.MobileDraining || s.PeerDemandFresh && s.PeerDraining || s.InteractiveQueueLimited || badPath || retries
	healthy := contiguous && pending && progressing && fresh >= min(3, active) && active == len(grant) && controlHealthy && !pressure
	if healthy {
		w.healthy++
	} else {
		w.healthy = 0
	}
	if contiguous && !w.demand {
		w.quiet++
	} else {
		w.quiet = 0
	}
	if contiguous && (pressure || (pending && !progressing)) {
		w.bad++
	} else {
		w.bad = 0
	}
	if !w.lastChange.IsZero() && now.Sub(w.lastChange) < elasticGrantCooldown {
		return false
	}
	// Extra paths can overlap a quiet user's baseline, never displace it. If
	// the owner becomes busy, relinquish the loan at the next safe transition.
	reclaim := -1
	for i := baseline; i < len(grant); i++ {
		for peer, other := range a.grants {
			if peer == id || !slices.Contains(other, grant[i]) {
				continue
			}
			state := a.adaptive[peer]
			if state == nil || now.Sub(state.at) > 5*time.Second || state.demand {
				reclaim = i
				break
			}
		}
		if reclaim >= 0 {
			break
		}
	}
	if len(grant) > baseline && (reclaim >= 0 || w.bad >= 3 || w.quiet >= 5) {
		if reclaim < 0 {
			reclaim = len(grant) - 1
		}
		next := slices.Clone(grant)
		a.grants[id] = slices.Delete(next, reclaim, reclaim+1)
		w.lastChange = now
		w.healthy = 0
		return true
	}
	if w.healthy < 3 || len(grant) >= n {
		return false
	}
	// First prefer an unassigned account; only borrow an account from another
	// authenticated user if that user has fresh, sustained idle evidence.
	for _, shared := range []bool{false, true} {
		for _, account := range a.accounts {
			if slices.Contains(grant, account) {
				continue
			}
			l, ok := byID[account]
			if !ok || !l.Ready || l.Failure != "" || l.BulkBlocked || l.PeerUnavailable || l.PeerStoragePressure || l.BulkHeadroomBytes < storageGrantMinimumHeadroom {
				continue
			}
			occupied, allowed := false, true
			for peer, other := range a.grants {
				if peer == id || !slices.Contains(other, account) {
					continue
				}
				occupied = true
				state := a.adaptive[peer]
				if state == nil || now.Sub(state.at) > 5*time.Second || state.quiet < 5 || state.demand {
					allowed = false
				}
			}
			if occupied != shared || !allowed {
				continue
			}
			a.grants[id] = append(slices.Clone(grant), account)
			w.lastChange = now
			w.healthy = 0
			return true
		}
	}
	return false
}
