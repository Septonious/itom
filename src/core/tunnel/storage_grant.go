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
	blocked map[string]int
	last    time.Time
}

func (a *sessionAdmission) observeStorage(identity string, mail mailstream.MailSnapshot, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	grant := a.grants[identity]
	if len(a.accounts) < 10 || a.owners[identity] == 0 || len(grant) < 2 || !mail.GrantNegotiated || mail.GrantChanging || mail.AllocatedAccounts != len(grant) {
		return false
	}
	// Refuse to act while an earlier change is awaiting peer acknowledgement.
	inventory := make(map[string]mailstream.MailAccountSnapshot, len(mail.Lines))
	for _, line := range mail.Lines {
		inventory[line.Account] = line
		if line.Allocated != slices.Contains(grant, line.Account) {
			return false
		}
	}
	if a.storage == nil {
		a.storage = map[string]*storageGrantWatch{}
	}
	w := a.storage[identity]
	if w == nil {
		w = &storageGrantWatch{blocked: map[string]int{}}
		a.storage[identity] = w
	}
	for _, id := range grant {
		line, ok := inventory[id]
		if !ok {
			return false
		}
		if line.BulkHeadroomBytes < 512<<10 || line.BulkBlocked && (line.StoragePressure || line.PeerStoragePressure) {
			w.blocked[id]++
		} else {
			delete(w.blocked, id)
		}
	}
	if !w.last.IsZero() && now.Sub(w.last) < storageGrantCooldown {
		return false
	}
	occupied := map[string]bool{}
	for _, g := range a.grants {
		for _, id := range g {
			occupied[id] = true
		}
	}
	var spare string
	var best int
	for _, id := range preferredGrantAccounts(mail.Lines) {
		line := inventory[id]
		if occupied[id] || !slices.Contains(a.accounts, id) || !line.Ready || line.Failure != "" || line.BulkBlocked || line.PeerStoragePressure || line.BulkHeadroomBytes < storageGrantMinimumHeadroom {
			continue
		}
		spare, best = id, line.BulkHeadroomBytes
		break
	}
	if spare == "" {
		return false
	}
	worst := -1
	for i, id := range grant {
		line := inventory[id]
		if w.blocked[id] >= 3 && best-line.BulkHeadroomBytes >= storageGrantMinimumHeadroom && (worst < 0 || line.BulkHeadroomBytes < inventory[grant[worst]].BulkHeadroomBytes) {
			worst = i
		}
	}
	if worst < 0 {
		return false
	}
	next := slices.Clone(grant)
	next[worst] = spare
	a.grants[identity] = next
	w.last = now
	clear(w.blocked)
	return true
}
