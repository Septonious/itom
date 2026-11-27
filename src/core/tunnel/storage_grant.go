package tunnel

import (
	mailstream "itom.local/mailstream"
	"slices"
	"time"
)

// A single blocked account can persist for many seconds while collection
// retries. Rotate at most one such account per 30 seconds, after three fresh
// observations. No debt is forgiven; the existing grant handshake handles
// make-before-break and the normal account count remains unchanged.
const storageGrantCooldown = 30 * time.Second
const storageGrantMinimumHeadroom = 8 << 20

type storageGrantWatch struct {
	blocked     map[string]int
	unavailable map[string]int
	last        time.Time
}

func (a *sessionAdmission) observeStorage(identity string, mail mailstream.MailSnapshot, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	grant := a.grants[identity]
	if len(a.accounts) < 10 || a.owners[identity] == 0 || len(grant) < 2 || !mail.GrantNegotiated {
		return false
	}
	// During an unfinished change only repair availability; never rotate for
	// storage pressure. Preserve the union until the next acknowledged grant.
	changing := mail.GrantChanging
	if !changing && mail.AllocatedAccounts != len(grant) {
		return false
	}
	inventory := make(map[string]mailstream.MailAccountSnapshot, len(mail.Lines))
	for _, line := range mail.Lines {
		inventory[line.Account] = line
		if !changing && line.Allocated != slices.Contains(grant, line.Account) {
			return false
		}
	}
	if a.storage == nil {
		a.storage = map[string]*storageGrantWatch{}
	}
	w := a.storage[identity]
	if w == nil {
		w = &storageGrantWatch{blocked: map[string]int{}, unavailable: map[string]int{}}
		a.storage[identity] = w
	}
	for _, id := range grant {
		line, ok := inventory[id]
		if !ok {
			return false
		}
		// The server keeps its readers warm even when the client parks accounts.
		if !line.Ready || line.PeerUnavailable {
			w.unavailable[id]++
		} else {
			delete(w.unavailable, id)
		}
		if line.BulkHeadroomBytes < 512<<10 || line.BulkBlocked && (line.StoragePressure || line.PeerStoragePressure) {
			w.blocked[id]++
		} else {
			delete(w.blocked, id)
		}
	}
	occupied := map[string]bool{}
	for _, g := range a.grants {
		for _, id := range g {
			occupied[id] = true
		}
	}
	// Replace persistent unavailable paths in one authenticated update. A
	// single replacement could leave another dead account in the desired set,
	// preventing either peer from completing the all-account preparation.
	next := slices.Clone(grant)
	replaced := false
	pick := func(old string, storage bool) string {
		for _, id := range preferredGrantAccounts(mail.Lines) {
			line := inventory[id]
			if occupied[id] || !slices.Contains(a.accounts, id) || !line.Ready || line.Failure != "" || line.BulkBlocked || line.PeerStoragePressure || line.PeerUnavailable || line.BulkHeadroomBytes < storageGrantMinimumHeadroom {
				continue
			}
			if storage && line.BulkHeadroomBytes-inventory[old].BulkHeadroomBytes < storageGrantMinimumHeadroom {
				continue
			}
			return id
		}
		return ""
	}
	for i, id := range grant {
		if w.unavailable[id] < 3 {
			continue
		}
		spare := pick(id, false)
		if spare == "" {
			return false
		}
		next[i], occupied[spare], replaced = spare, true, true
	}
	if !replaced {
		if changing {
			return false
		}
		if !w.last.IsZero() && now.Sub(w.last) < storageGrantCooldown {
			return false
		}
		worst := -1
		for i, id := range grant {
			if w.blocked[id] >= 3 && (worst < 0 || inventory[id].BulkHeadroomBytes < inventory[grant[worst]].BulkHeadroomBytes) {
				worst = i
			}
		}
		if worst < 0 {
			return false
		}
		spare := pick(grant[worst], true)
		if spare == "" {
			return false
		}
		next[worst] = spare
	}
	a.grants[identity] = next
	w.last = now
	clear(w.blocked)
	clear(w.unavailable)
	return true
}
