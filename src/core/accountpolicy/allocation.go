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
