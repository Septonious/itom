package tunnel

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/servercfg"
	"itom.local/core/transport"
)

// Compatibility describes only the installed disk schema. No scheduling,
// retransmission, timing or transport implementation is imported from an RC.
func diskConfig(a servercfg.Account, u servercfg.User, limit pool.Amount, shards int) journal.Config {
	folders := []string{u.Up, u.Down}
	for _, base := range []string{u.Up, u.Down} {
		for n := 0; n < shards; n++ {
			folders = append(folders, fmt.Sprintf("%s-data-%02d", base, n))
		}
	}
	return journal.Config{MaxGrants: 256, MaxAttempts: 256, Accounts: []journal.Account{{ID: a.ID, Provider: a.Mail.Host, Limit: limit, Users: []journal.User{{ID: u.ID, Limit: limit, Folders: folders}}}}}
}

func openIssuer(root, name string, a servercfg.Account, u servercfg.User) (*journal.Store, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	limit := pool.Amount{Bytes: 2 << 20, Messages: 128}
	var alternatives []journal.Config
	if name == "data" {
		limit = pool.Amount{Bytes: 12 << 20, Messages: 192}
		for _, capacity := range []pool.Amount{{Bytes: 8 << 20, Messages: 128}, {Bytes: 12 << 20, Messages: 192}, {Bytes: 16 << 20, Messages: 256}} {
			for _, shards := range []int{0, 1, 2, 4} {
				if capacity == limit && shards == 0 {
					continue
				}
				alternatives = append(alternatives, diskConfig(a, u, capacity, shards))
			}
		}
	}
	return journal.OpenCompatible(filepath.Join(root, name+".db"), diskConfig(a, u, limit, 0), alternatives, time.Second)
}

func MigrateDataProfile(root string, a servercfg.Account, u servercfg.User, profile string) error {
	limits := map[string]pool.Amount{"data8": {Bytes: 8 << 20, Messages: 128}, "data12": {Bytes: 12 << 20, Messages: 192}, "data16": {Bytes: 16 << 20, Messages: 256}}
	limit, ok := limits[profile]
	if !ok {
		return journal.ErrConfig
	}
	if _, err := os.Stat(filepath.Join(root, "data.db")); err != nil {
		return err
	}
	s, err := openIssuer(root, "data", a, u)
	if err != nil {
		return err
	}
	defer s.Close()
	cfg := s.Config()
	cfg.Accounts[0].Limit = limit
	cfg.Accounts[0].Users[0].Limit = limit
	return s.MigrateConfig(cfg)
}

func sessionPlan(p servercfg.Profile) (transport.RosterPlan, transport.InvitationTemplate, error) {
	var template transport.InvitationTemplate
	cfg := servercfg.Config{Schema: 1, Server: p.Server, Provider: p.Provider, Users: []servercfg.User{p.User}, Accounts: p.Accounts, Transport: p.Transport}
	if p.Schema != 1 || p.Kind != "itom-imap-probe" || !p.User.Enabled || len(p.Accounts) == 0 || cfg.Validate() != nil {
		return transport.RosterPlan{}, template, servercfg.ErrConfig
	}
	t := p.Transport.Effective()
	n := len(p.Accounts)
	plan := transport.RosterPlan{ClientBudget: transport.RuntimeBudget{Connections: 2 * n, PerAccount: 2}, ServerBudget: transport.RuntimeBudget{Connections: 2 * n, PerAccount: 2}}
	if 2*n*t.Participants > t.EndpointConnections || 4*t.Participants > t.AccountSessions {
		return plan, template, ErrProfileCapacity
	}
	for _, a := range p.Accounts {
		if a.Mail.TLS != "tls" {
			return plan, template, errors.New("V2_TLS_IMAP_REQUIRED")
		}
		plan.Accounts = append(plan.Accounts, transport.RosterAccount{Account: a.ID, FastFolder: p.User.Up, BulkFolder: p.User.Down})
	}
	sort.Slice(plan.Accounts, func(i, j int) bool { return plan.Accounts[i].Account < plan.Accounts[j].Account })
	// Bind account identities/folder selection and shared budgets, never keys,
	// mutable provider metrics, credentials or a hardcoded route latency.
	raw, _ := json.Marshal(struct {
		Server [16]byte
		User   string
		Plan   transport.RosterPlan
	}{p.Server, p.User.ID, plan})
	hash := sha256.Sum256(append([]byte("ITOM/endpoint/v2/config\x00"), raw...))
	pair := sha256.Sum256(append([]byte("ITOM/endpoint/v2/pair\x00"), raw...))
	template = transport.InvitationTemplate{Binding: secure.Binding{Server: p.Server, User: p.User.ID, Epoch: 1, ConfigHash: hash}, Generation: 1}
	copy(template.Binding.Pair[:], pair[:16])
	return plan, template, nil
}

func selectProfile(ctx context.Context, p servercfg.Profile, plan transport.RosterPlan, journals []transport.RosterJournals, d transport.NetworkDialer, tlsConfig *tls.Config, onSelected func(), observe func(selectionEvent)) ([]transport.RosterLane, error) {
	key, err := hex.DecodeString(p.User.Key)
	if err != nil {
		return nil, servercfg.ErrConfig
	}
	defer clear(key)
	byAccount := map[string]servercfg.Account{}
	for _, a := range p.Accounts {
		byAccount[a.ID] = a
	}
	byJournal := map[string]transport.RosterJournals{}
	for _, j := range journals {
		byJournal[j.Account] = j
	}
	server := len(journals) > 0
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan transport.RosterAccount, len(plan.Accounts))
	for _, a := range plan.Accounts {
		jobs <- a
	}
	close(jobs)
	type opened struct {
		lanes []transport.RosterLane
		err   error
	}
	results := make(chan opened, len(plan.Accounts))
	var wg sync.WaitGroup
	for range min(p.Transport.Effective().LoginParallelism, len(plan.Accounts)) {
		wg.Go(func() {
			for a := range jobs {
				var lanes []transport.RosterLane
				open := func(folder string) (*transport.Executor, error) {
					role := "retained"
					if folder == a.FastFolder {
						role = "fast"
					} else if folder == a.BulkFolder {
						role = "bulk"
					}
					note := func(stage string, e error, plus, keywords bool) {
						if observe != nil {
							observe(selectionEvent{Account: a.Account, Role: role, Stage: stage, Failure: startupFailureCode(e), UIDPlus: plus, Keywords: keywords})
						}
					}
					note("imap_connecting", nil, false, false)
					account := byAccount[a.Account]
					host := account.Mail.Host
					if account.Mail.DialIP != "" {
						host = account.Mail.DialIP
					}
					selected, e := transport.DialSelected(ctx, d, transport.Endpoint{Address: net.JoinHostPort(host, strconv.Itoa(account.Mail.Port)), ServerName: account.Mail.Host, Username: account.Mail.Username, Password: account.Mail.Password, Mailbox: folder, AccountID: a.Account, TLS: tlsConfig, CreateMailbox: server}, transport.DefaultIMAPLimits())
					if e != nil {
						note("imap_"+transport.IMAPStartupStage(e), e, false, false)
						return nil, e
					}
					if !selected.Keywords || !selected.UIDPlus {
						selected.Executor.Close()
						e = errProviderKeywords
						if !selected.UIDPlus {
							e = errProviderUIDPlus
						}
						note("imap_capabilities", e, selected.UIDPlus, selected.Keywords)
						return nil, e
					}
					note("imap_selected", nil, selected.UIDPlus, selected.Keywords)
					return selected.Executor, nil
				}
				var failure error
				if server {
					// Scan every retained configured folder before opening the two runtime
					// sockets. This keeps the same per-account socket budget throughout.
					folders := map[string]bool{}
					j := byJournal[a.Account]
					for _, s := range []*journal.Store{j.Control, j.Data} {
						for _, account := range s.Config().Accounts {
							for _, u := range account.Users {
								for _, f := range u.Folders {
									folders[f] = true
								}
							}
						}
					}
					for folder := range folders {
						x, e := open(folder)
						if e != nil {
							failure = e
							break
						}
						e = transport.InventoryRetained(ctx, x, p.User.ID, key)
						x.Close()
						if e != nil {
							if observe != nil {
								observe(selectionEvent{Account: a.Account, Stage: "inventory_failed", Role: "retained", Failure: startupFailureCode(e)})
							}
							failure = e
							break
						}
					}
					if failure == nil {
						for _, s := range []*journal.Store{j.Control, j.Data} {
							if e := s.EnableAfterInventory(a.Account, p.User.ID, s.Run()); e != nil {
								failure = e
								break
							}
						}
					}
				}
				if failure == nil {
					for _, l := range []struct {
						plane  transport.Plane
						folder string
					}{{transport.Fast, a.FastFolder}, {transport.Bulk, a.BulkFolder}} {
						x, e := open(l.folder)
						if e != nil {
							failure = e
							break
						}
						lanes = append(lanes, transport.RosterLane{Plane: l.plane, Executor: x})
						if onSelected != nil {
							onSelected()
						}
					}
				}
				if failure != nil {
					cancel()
				}
				results <- opened{lanes, failure}
			}
		})
	}
	wg.Wait()
	close(results)
	var lanes []transport.RosterLane
	for r := range results {
		lanes = append(lanes, r.lanes...)
		err = errors.Join(err, r.err)
	}
	if err != nil {
		for _, l := range lanes {
			l.Executor.Close()
		}
		return nil, err
	}
	return lanes, nil
}

func openProfileJournals(root string, p servercfg.Profile) ([]transport.RosterJournals, error) {
	var out []transport.RosterJournals
	for _, a := range p.Accounts {
		j := transport.RosterJournals{Account: a.ID}
		var err error
		j.Control, err = openIssuer(filepath.Join(root, a.ID, p.User.ID), "control", a, p.User)
		if err == nil {
			j.Data, err = openIssuer(filepath.Join(root, a.ID, p.User.ID), "data", a, p.User)
		}
		out = append(out, j)
		if err != nil {
			closeProfileJournals(out)
			return nil, err
		}
	}
	return out, nil
}
func closeProfileJournals(journals []transport.RosterJournals) {
	for _, j := range journals {
		if j.Control != nil {
			j.Control.Close()
		}
		if j.Data != nil {
			j.Data.Close()
		}
	}
}

func walletPaths(root string, p servercfg.Profile, plan transport.RosterPlan) (string, []transport.RosterWalletPaths, error) {
	var paths []transport.RosterWalletPaths
	var anchor string
	for index, a := range plan.Accounts {
		dir := filepath.Join(root, a.Account)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", nil, err
		}
		path := transport.RosterWalletPaths{Account: a.Account, Control: filepath.Join(dir, "control-wallet.db"), Data: filepath.Join(dir, "wallet.db")}
		if index == 0 {
			anchor = path.Control
			path.Control = ""
		}
		paths = append(paths, path)
	}
	return anchor, paths, nil
}
