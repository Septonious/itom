// Package accountpolicy computes account grants. It does not own the mail
// transport, revoke grants or close connections. Callers must apply changes
// through an authenticated, generation-safe draining lifecycle.
package accountpolicy

import "slices"

// Plan returns deterministic account assignments for distinct online users.
// A single user receives ceil(N/2); multiple users split the remainder fairly.
// Below ten accounts the controller is disabled, and when the five-account
// minimum exceeds capacity users share physical accounts on distinct lanes.
// Inputs are not mutated. Empty and duplicate identities are ignored.
func Plan(accounts, online []string) map[string][]string {
	accounts, online = unique(accounts), unique(online)
	grants := make(map[string][]string, len(online))
	n, u := len(accounts), len(online)
	if u == 0 {
		return grants
	}
	offset := 0
	for i, id := range online {
		count := n
		if n >= 10 {
			if u == 1 {
				count = (n + 1) / 2
			} else {
				count = n / u
				if i < n%u {
					count++
				}
				count = max(5, count)
			}
		}
		grant := make([]string, 0, count)
		for j := 0; j < count; j++ {
			grant = append(grant, accounts[(offset+j)%n])
		}
		grants[id] = grant
		if n > 0 {
			offset = (offset + count) % n
		}
	}
	return grants
}

func unique(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	result = slices.Compact(result)
	if len(result) > 0 && result[0] == "" {
		result = result[1:]
	}
	return result
}

// InitialGrant assigns a newly admitted session while preserving existing grants.
// Existing users remain pinned until a generation-safe live resize is implemented.
// Unused accounts are preferred; sharing is allowed to satisfy the minimum.
func InitialGrant(accounts []string, existing map[string][]string) []string {
	accounts = unique(accounts)
	n, u := len(accounts), len(existing)+1
	if n < 10 {
		return accounts
	}
	count := max(5, n/u)
	if u == 1 {
		count = (n + 1) / 2
	}
	used := map[string]bool{}
	for _, grant := range existing {
		for _, id := range grant {
			used[id] = true
		}
	}
	result := make([]string, 0, count)
	for _, shared := range []bool{false, true} {
		for _, id := range accounts {
			if used[id] == shared && len(result) < count {
				result = append(result, id)
			}
		}
	}
	return result
}

// Rebalance preserves as many existing exclusive assignments as possible,
// allocating the remainder in stable admission order. Shared mailboxes are
// used only when the five-account minimum requires them.
func Rebalance(accounts, online []string, previous map[string][]string) map[string][]string {
	accounts = unique(accounts)
	n := len(accounts)
	seen := map[string]bool{}
	users := []string{}
	for _, u := range online {
		if u != "" && !seen[u] {
			seen[u] = true
			users = append(users, u)
		}
	}
	out := map[string][]string{}
	if len(users) == 0 {
		return out
	}
	if n < 10 {
		for _, u := range users {
			out[u] = slices.Clone(accounts)
		}
		return out
	}
	counts := map[string]int{}
	used := map[string]bool{}
	valid := map[string]bool{}
	for _, a := range accounts {
		valid[a] = true
	}
	for i, u := range users {
		k := n / len(users)
		if i < n%len(users) {
			k++
		}
		if len(users) == 1 {
			k = (n + 1) / 2
		}
		counts[u] = max(5, k)
		out[u] = []string{}
		for _, a := range previous[u] {
			if valid[a] && !used[a] && len(out[u]) < counts[u] {
				out[u] = append(out[u], a)
				used[a] = true
			}
		}
	}
	for _, u := range users {
		for _, a := range accounts {
			if !used[a] && len(out[u]) < counts[u] {
				out[u] = append(out[u], a)
				used[a] = true
			}
		}
	}
	for _, u := range users {
		for _, a := range accounts {
			if len(out[u]) < counts[u] && !slices.Contains(out[u], a) {
				out[u] = append(out[u], a)
			}
		}
	}
	return out
}
