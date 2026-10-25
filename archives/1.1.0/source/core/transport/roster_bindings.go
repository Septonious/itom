package transport

import (
	"errors"
	"path/filepath"

	"itom.local/core/journal"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// RosterJournals borrows original already-open account/user issuers. No files
// are created, replaced or combined by server roster construction.
type RosterJournals struct {
	Account       string
	Control, Data *journal.Store
}
type RosterWalletPaths struct{ Account, Control, Data string }
type RosterWallets struct{ Control, Data *wallet.Wallet }

func NewServerAccountRoster(h *BootstrapHandoff, plan RosterPlan, lanes []RosterLane, bindings []RosterJournals, key []byte) (*RosterDriver, error) {
	if h == nil || h.epoch == nil || h.client || len(bindings) != len(plan.Accounts) {
		return nil, ErrProtocol
	}
	d := &RosterDriver{input: h, send: 1, journals: map[string]RosterJournals{}}
	for _, b := range bindings {
		if b.Control == nil || b.Data == nil || b.Control.StoreID() == b.Data.StoreID() {
			return nil, ErrProtocol
		}
		if _, ok := d.journals[b.Account]; ok {
			return nil, ErrProtocol
		}
		d.journals[b.Account] = b
	}
	for _, a := range plan.Accounts {
		b, ok := d.journals[a.Account]
		if !ok {
			return nil, ErrProtocol
		}
		for _, entry := range []struct {
			s       *journal.Store
			folders []string
		}{{b.Control, []string{a.FastFolder}}, {b.Data, []string{a.FastFolder, a.BulkFolder}}} {
			if _, err := entry.s.Limit(a.Account, h.origin.User); err != nil {
				return nil, ErrProtocol
			}
			found := false
			for _, account := range entry.s.Config().Accounts {
				if account.ID != a.Account {
					continue
				}
				for _, u := range account.Users {
					if u.ID != h.origin.User {
						continue
					}
					fs := map[string]bool{}
					for _, f := range u.Folders {
						fs[f] = true
					}
					found = true
					for _, f := range entry.folders {
						found = found && fs[f]
					}
				}
			}
			if !found {
				return nil, ErrProtocol
			}
		}
	}
	anchor, ok := d.journals[h.origin.Account]
	if !ok || anchor.Control.StoreID() != h.origin.Store || anchor.Control.Run() != h.origin.Run {
		return nil, ErrProtocol
	}
	d.store = anchor.Control
	d.manifest = RosterManifest{ControlStore: h.origin.Store, ControlRun: h.origin.Run, DataStore: anchor.Data.StoreID(), DataRun: anchor.Data.Run(), ClientBudget: plan.ClientBudget, ServerBudget: plan.ServerBudget}
	return newRoster(d, plan, lanes, key)
}

// Paths are supplied locally by the persistent identity owner, never by mail.
// The anchor CONTROL wallet is already open for the fresh Noise session; every
// other CONTROL and DATA file is opened in place only after authenticating the
// complete per-account offer. Partial failure sends no ACCEPT and deletes none.
func NewClientAccountRoster(h *BootstrapHandoff, plan RosterPlan, lanes []RosterLane, anchor *wallet.Wallet, paths []RosterWalletPaths, key []byte) (*RosterDriver, error) {
	if h == nil || h.epoch == nil || !h.client || anchor == nil || len(paths) != len(plan.Accounts) {
		return nil, ErrProtocol
	}
	id := anchor.Identity()
	b := h.epoch.Context()
	if id.Store != h.origin.Store || id.User != b.User || id.Session != b.Session {
		return nil, ErrProtocol
	}
	d := &RosterDriver{input: h, client: true, wallet: anchor, receive: 1, walletPaths: map[string]RosterWalletPaths{}}
	seen := map[string]bool{}
	for _, p := range paths {
		if _, ok := d.walletPaths[p.Account]; ok {
			return nil, ErrProtocol
		}
		if p.Data == "" || (p.Account == h.origin.Account) != (p.Control == "") {
			return nil, ErrProtocol
		}
		for _, name := range []string{p.Control, p.Data} {
			if name == "" {
				continue
			}
			path, err := filepath.Abs(name)
			if err != nil || seen[path] {
				return nil, ErrProtocol
			}
			seen[path] = true
		}
		d.walletPaths[p.Account] = p
	}
	for _, a := range plan.Accounts {
		if _, ok := d.walletPaths[a.Account]; !ok {
			return nil, ErrProtocol
		}
	}
	return newRoster(d, plan, lanes, key)
}
func (r *RosterResult) Wallets(account string) (RosterWallets, bool) {
	w, ok := r.wallets[account]
	return w, ok
}
func (r *RosterResult) Journals(account string) (RosterJournals, bool) {
	j, ok := r.journals[account]
	return j, ok
}
func (r *RosterResult) CloseWallets() error {
	r.walletClose.Do(func() {
		for _, w := range r.ownedWallets {
			r.walletCloseErr = errors.Join(r.walletCloseErr, w.Close())
		}
	})
	return r.walletCloseErr
}
func (d *RosterDriver) openWallets(m RosterManifest) error {
	d.wallets = make(map[string]RosterWallets, len(m.Accounts))
	if d.walletPaths == nil {
		// Explicit shared-file adapter retained for shared original issuer scopes.
		for _, a := range m.Accounts {
			if a.ControlStore != m.ControlStore || a.ControlRun != m.ControlRun || a.DataStore != m.DataStore || a.DataRun != m.DataRun {
				return ErrProtocol
			}
		}
		w, err := wallet.OpenBound(d.dataPath, m.DataStore, d.input.epoch)
		if err != nil {
			return err
		}
		d.dataWallet = w
		d.ownedWallets = append(d.ownedWallets, w)
		for _, a := range m.Accounts {
			d.wallets[a.Account] = RosterWallets{Control: d.wallet, Data: w}
		}
		return nil
	}
	for _, a := range m.Accounts {
		paths := d.walletPaths[a.Account]
		control := d.wallet
		if a.Account != d.input.origin.Account {
			var err error
			control, err = wallet.OpenBound(paths.Control, a.ControlStore, d.input.epoch)
			if err != nil {
				return err
			}
			d.ownedWallets = append(d.ownedWallets, control)
		}
		data, err := wallet.OpenBound(paths.Data, a.DataStore, d.input.epoch)
		if err != nil {
			return err
		}
		d.ownedWallets = append(d.ownedWallets, data)
		d.wallets[a.Account] = RosterWallets{Control: control, Data: data}
		if a.Account == d.input.origin.Account {
			d.dataWallet = data
		}
	}
	if d.dataWallet == nil {
		return ErrProtocol
	}
	return nil
}

func validRosterAnchor(m RosterManifest, origin ServerOrigin) bool {
	a, ok := m.Account(origin.Account)
	return ok && a.ControlStore == origin.Store && a.ControlRun == origin.Run && m.ControlStore == a.ControlStore && m.ControlRun == a.ControlRun && m.DataStore == a.DataStore && m.DataRun == a.DataRun && a.DataStore != (wire.ID{})
}
