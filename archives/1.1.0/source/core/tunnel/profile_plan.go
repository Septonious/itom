package tunnel

import (
	"errors"
	"sort"

	"itom.local/core/journal"
	"itom.local/core/servercfg"
	"itom.local/core/transport"
	"itom.local/core/wire"
)

var ErrProfileService = errors.New("V2_PROFILE_SERVICE_SCOPE")
var ErrProfileCapacity = errors.New("V2_PROFILE_SERVICE_CAPACITY")
var ErrPerAccountRoster = errors.New("V2_PER_ACCOUNT_ROSTER_REQUIRED")

// AccountJournals identifies the original already-open per-account/user files.
// Planning never opens a substitute file or combines their budgets/identities.
type AccountJournals struct {
	Account       string
	Control, Data *journal.Store
}

type AccountServicePlan struct {
	transport.RosterAccount
	ControlStore, DataStore wire.ID
	ControlRun, DataRun     uint64
	// InventoryFolders includes retained legacy/shard folders, not just lanes.
	// Selection is not proof that this complete inventory has actually happened.
	InventoryFolders []string
}

type ProfileServicePlan struct {
	Accounts                   []AccountServicePlan
	ClientBudget, ServerBudget transport.RuntimeBudget
}

// PlanProfileService maps a validated profile onto the ORIGINAL journal scopes.
// It grants no readiness or permission and changes no journal configuration.
// Explicit endpoint budgets must fit both the endpoint cap and shared login
// budget for every configured participant. Configured accounts are not truncated
// to whichever sockets happened to connect first.
func PlanProfileService(p servercfg.Profile, bindings []AccountJournals, client, server transport.RuntimeBudget) (ProfileServicePlan, error) {
	var result ProfileServicePlan
	cfg := servercfg.Config{Schema: 1, Server: p.Server, Provider: p.Provider, Users: []servercfg.User{p.User}, Accounts: p.Accounts, Transport: p.Transport}
	if p.Schema != 1 || p.Kind != "itom-imap-probe" || !p.User.Enabled || len(p.Accounts) == 0 || cfg.Validate() != nil {
		return result, ErrProfileService
	}
	t := p.Transport.Effective()
	n := len(p.Accounts)
	for _, b := range []transport.RuntimeBudget{client, server} {
		if b.Connections < 2*n || b.Connections > t.EndpointConnections || b.Connections > 4096 || b.PerAccount < 2 || b.PerAccount > 128 {
			return result, ErrProfileCapacity
		}
	}
	// Reserve the same allocation for every participant; a per-user planner has
	// no authority to borrow another user's share of the provider login ceiling.
	if (client.PerAccount+server.PerAccount)*t.Participants > t.AccountSessions || server.Connections*t.Participants > t.EndpointConnections {
		return result, ErrProfileCapacity
	}
	if len(bindings) != n {
		return result, ErrProfileService
	}
	byAccount := make(map[string]AccountJournals, n)
	for _, b := range bindings {
		if b.Control == nil || b.Data == nil || b.Control.StoreID() == b.Data.StoreID() {
			return result, ErrProfileService
		}
		if _, duplicate := byAccount[b.Account]; duplicate {
			return result, ErrProfileService
		}
		byAccount[b.Account] = b
	}
	for _, a := range p.Accounts {
		b, ok := byAccount[a.ID]
		if !ok {
			return ProfileServicePlan{}, ErrProfileService
		}
		if _, err := b.Control.Limit(a.ID, p.User.ID); err != nil {
			return ProfileServicePlan{}, ErrProfileService
		}
		if _, err := b.Data.Limit(a.ID, p.User.ID); err != nil {
			return ProfileServicePlan{}, ErrProfileService
		}
		control, ok := profileFolders(b.Control.Config(), a.ID, p.User.ID, p.Provider)
		if !ok {
			return ProfileServicePlan{}, ErrProfileService
		}
		data, ok := profileFolders(b.Data.Config(), a.ID, p.User.ID, p.Provider)
		if !ok {
			return ProfileServicePlan{}, ErrProfileService
		}
		// Planning accepts only already-migrated, isolated session folders.
		// The offline journal owner adds them; this read-only planner cannot.
		fast := p.User.Up + sessionFolderSuffix
		if !control[fast] || !data[fast] {
			return ProfileServicePlan{}, ErrProfileService
		}
		bulk := p.User.Down + sessionFolderSuffix
		if bulk == fast || !data[bulk] {
			return ProfileServicePlan{}, ErrProfileService
		}
		union := make(map[string]bool, len(control)+len(data))
		for f := range control {
			union[f] = true
		}
		for f := range data {
			union[f] = true
		}
		if len(union) > journal.MaxFoldersPerUser {
			return ProfileServicePlan{}, ErrProfileCapacity
		}
		entry := AccountServicePlan{RosterAccount: transport.RosterAccount{Account: a.ID, FastFolder: fast, BulkFolder: bulk}, ControlStore: b.Control.StoreID(), ControlRun: b.Control.Run(), DataStore: b.Data.StoreID(), DataRun: b.Data.Run()}
		for folder := range union {
			entry.InventoryFolders = append(entry.InventoryFolders, folder)
		}
		sort.Strings(entry.InventoryFolders)
		result.Accounts = append(result.Accounts, entry)
	}
	sort.Slice(result.Accounts, func(i, j int) bool { return result.Accounts[i].Account < result.Accounts[j].Account })
	result.ClientBudget, result.ServerBudget = client, server
	return result, nil
}
func profileFolders(c journal.Config, account, user, provider string) (map[string]bool, bool) {
	for _, a := range c.Accounts {
		if a.ID != account {
			continue
		}
		if a.Provider != provider {
			return nil, false
		}
		for _, u := range a.Users {
			if u.ID == user {
				folders := make(map[string]bool, len(u.Folders))
				for _, f := range u.Folders {
					folders[f] = true
				}
				return folders, true
			}
		}
	}
	return nil, false
}

// SharedRosterPlan is only an adapter for scopes which actually share both
// original issuers. Separate installed per-account files require per-account
// wire authority; their identities cannot be flattened into an anchor's IDs.
func (p ProfileServicePlan) SharedRosterPlan() (transport.RosterPlan, error) {
	var r transport.RosterPlan
	if len(p.Accounts) == 0 {
		return r, ErrProfileService
	}
	first := p.Accounts[0]
	for _, a := range p.Accounts {
		if a.ControlStore != first.ControlStore || a.ControlRun != first.ControlRun || a.DataStore != first.DataStore || a.DataRun != first.DataRun {
			return transport.RosterPlan{}, ErrPerAccountRoster
		}
		r.Accounts = append(r.Accounts, a.RosterAccount)
	}
	r.ClientBudget, r.ServerBudget = p.ClientBudget, p.ServerBudget
	return r, nil
}

// RosterSelection retains all accounts even when their original issuers differ.
// NewServerAccountRoster consumes the matching original journal bindings; no
// identity is represented by substituting an anchor store for another file.
func (p ProfileServicePlan) RosterSelection() transport.RosterPlan {
	r := transport.RosterPlan{ClientBudget: p.ClientBudget, ServerBudget: p.ServerBudget}
	for _, a := range p.Accounts {
		r.Accounts = append(r.Accounts, a.RosterAccount)
	}
	return r
}
