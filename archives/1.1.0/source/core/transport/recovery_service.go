package transport

import (
	"errors"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"time"
)

// Historical launches yield during the original short-request lifetime. This
// changes scheduling, never a request deadline, permission or recovery proof.
// Current grants and replies to already admitted recovery are always serviced.
// Under bulk pressure recovery keeps running so capacity cannot be stranded.
func (s *RosterService) historicalLaunchAllowed() bool {
	if s.Runtime == nil {
		return true
	} // policy component construction
	return s.Runtime.session.historicalLaunchAllowed(time.Now()) || s.Runtime.permissionPressure()
}
func (s *Session) historicalLaunchAllowed(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastForeground.IsZero() || now.Sub(s.lastForeground) >= openTimeout || s.buffered > uint64(s.limits.InteractiveBytes) || s.received > uint64(s.limits.InteractiveBytes)
}

// A single blocked account must not flood healthy accounts with historical
// traffic during a short request. Global recovery overrides foreground grace
// only when every eligible account in a ledger needs capacity. Local cleanup
// can still restore the affected account immediately, without forgiving debt.
func (r *Runtime) permissionPressure() bool {
	pressure := r.permissionPressureByAccount()
	for _, class := range []LedgerClass{ControlLedger, DataLedger} {
		eligible, blocked := 0, 0
		for _, ledgers := range pressure {
			if value, ok := ledgers[class]; ok {
				eligible++
				if value {
					blocked++
				}
			}
		}
		if eligible > 0 && eligible == blocked {
			return true
		}
	}
	return false
}

func (r *Runtime) accountPermissionPressure(account string) bool {
	for _, blocked := range r.permissionPressureByAccount()[account] {
		if blocked {
			return true
		}
	}
	return false
}

func (r *Runtime) permissionPressureByAccount() map[string]map[LedgerClass]bool {
	pressure := map[string]map[LedgerClass]bool{}
	accountLedgers := func(account string) map[LedgerClass]bool {
		if pressure[account] == nil {
			pressure[account] = map[LedgerClass]bool{}
		}
		return pressure[account]
	}
	for _, c := range r.carriers {
		w := c.Writer
		if w == nil || w.executor == nil {
			continue
		}
		ledgers, stats := accountLedgers(w.executor.account), w.Stats()
		for _, source := range w.sources {
			blocked := source.class == ControlLedger && stats.ControlBlocked || source.class == DataLedger && stats.DataBlocked
			// FAST and BULK share the same original DATA quota for this account.
			ledgers[source.class] = ledgers[source.class] || blocked
		}
	}
	r.session.mu.Lock()
	owners := r.session.authorities
	r.session.mu.Unlock()
	for account, owner := range owners {
		ledgers := accountLedgers(account)
		owner.mu.Lock()
		for class := range owner.routes {
			issue := owner.issues[class]
			ledgers[class] = ledgers[class] || errors.Is(issue.err, journal.ErrLimit) || errors.Is(issue.err, journal.ErrRecovery)
		}
		owner.mu.Unlock()
	}
	return pressure
}

// Short exchanges need a bounded control lease, not a bulk-sized reservation
// left behind on every VPN restart. This caps only new CONTROL reservations;
// actual bulk demand retains the original allowance and DATA is unchanged.
func (s *Session) controlAllowance(allowance pool.Amount) pool.Amount {
	s.mu.Lock()
	bulk := s.buffered > uint64(s.limits.InteractiveBytes) || s.received > uint64(s.limits.InteractiveBytes)
	s.mu.Unlock()
	if !bulk {
		allowance.Messages = min(allowance.Messages, 8)
	}
	return allowance
}
