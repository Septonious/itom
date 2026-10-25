package transport

import (
	"crypto/sha256"

	"itom.local/core/journal"
)

// RosterService assembles the same account-scoped runtime used by production
// integration and application tests. Its authorities borrow original durable
// stores/wallets. Construction issues no grant and reports no VPN readiness.
type RosterService struct {
	Runtime     *Runtime
	Dialer      *ClientDialer
	Authorities map[string]*AuthorityController
}

type RosterServiceOptions struct {
	ReceiveWindow uint64
	ExitWorkers   int
	Policy        ExitPolicy
	Dialer        NetworkDialer
}

// Stateful assembly consumes the completed roster once. A failed assembly
// closes its logical session and selected carriers, retaining original wallet
// files, journal handles and exact charges for the lifecycle/recovery owner.
func NewRosterService(r *RosterResult, s *Session, key []byte, options RosterServiceOptions) (service *RosterService, err error) {
	if r == nil || s == nil || r.handoff == nil || len(key) != 32 || sha256.Sum256(key) != r.handoff.keyHash {
		return nil, ErrProtocol
	}
	r.serviceMu.Lock()
	defer r.serviceMu.Unlock()
	if r.serviceClaimed {
		return nil, ErrProtocol
	}
	s.mu.Lock()
	fresh := !s.closed && s.runtime == nil && s.services == 0 && s.openRole == 0 && len(s.flows) == 0 && len(s.authorities) == 0 && s.generation == r.handoff.generation
	s.mu.Unlock()
	if !fresh {
		return nil, ErrProtocol
	}
	if options.ReceiveWindow == 0 || options.ReceiveWindow > s.limits.StreamBytes || r.client && (options.Policy != nil || options.Dialer != nil) || !r.client && (options.Policy == nil || options.Dialer == nil || options.ExitWorkers < 1 || options.ExitWorkers > 64) {
		return nil, ErrProtocol
	}
	r.serviceClaimed = true
	defer func() {
		if service == nil {
			s.Close()
			for _, l := range r.lanes {
				l.Executor.Close()
			}
		}
	}()
	c, err := NewCipher(r.handoff.epoch)
	if err != nil {
		return nil, err
	}
	svc := &RosterService{Authorities: map[string]*AuthorityController{}}
	var exit *ExitServer
	if r.client {
		svc.Dialer, err = NewClientDialer(s, options.ReceiveWindow)
	} else {
		exit, err = NewExitServer(s, c, options.ReceiveWindow, options.ExitWorkers, options.Policy, options.Dialer)
	}
	if err != nil {
		return nil, err
	}
	for _, a := range r.manifest.Accounts {
		var owner *AuthorityController
		if r.client {
			wallets, ok := r.Wallets(a.Account)
			if !ok {
				return nil, ErrProtocol
			}
			owner, err = NewClientAuthority(s, c, a.Account, ClientAuthorityLedger{Class: ControlLedger, Wallet: wallets.Control, Run: a.ControlRun}, ClientAuthorityLedger{Class: DataLedger, Wallet: wallets.Data, Run: a.DataRun})
		} else {
			stores, ok := r.Journals(a.Account)
			if !ok {
				return nil, ErrProtocol
			}
			owner, err = NewServerAuthority(s, c, a.Account, ServerAuthorityLedger{Class: ControlLedger, Store: stores.Control}, ServerAuthorityLedger{Class: DataLedger, Store: stores.Data})
		}
		if err != nil {
			return nil, err
		}
		svc.Authorities[a.Account] = owner
	}
	var carriers []RuntimeCarrier
	for _, l := range r.lanes {
		account := l.Executor.account
		a, ok := r.manifest.Account(account)
		if !ok {
			return nil, ErrProtocol
		}
		var inbox *Inbox
		var writer *PublicationWorker
		var cr []*ClientReceiver
		var rr []*RemoteReceiver
		var cp []*ClientPublisher
		var sp []*ServerPublisher
		for _, class := range []LedgerClass{ControlLedger, DataLedger} {
			if l.Plane == Bulk && class == ControlLedger {
				continue
			}
			if r.client {
				wallets := r.wallets[account]
				w := wallets.Data
				id, run := a.DataStore, a.DataRun
				if class == ControlLedger {
					w = wallets.Control
					id, run = a.ControlStore, a.ControlRun
				}
				var p *ClientPublisher
				p, err = NewClientPublisher(s, l.Plane, class, c, w, account, l.Executor.mailbox, key)
				if err != nil {
					return nil, err
				}
				cp = append(cp, p)
				var receive *ClientReceiver
				receive, err = NewClientReceiver(s, class, c, ServerOrigin{Store: id, Run: run, Account: account, User: r.handoff.origin.User, Folder: l.Executor.mailbox, UIDValidity: l.Executor.validity}, key)
				if err != nil {
					return nil, err
				}
				cr = append(cr, receive)
			} else {
				stores := r.journals[account]
				store := stores.Data
				if class == ControlLedger {
					store = stores.Control
				}
				scope := journal.Scope{Account: account, User: r.handoff.origin.User, Session: r.handoff.epoch.Context().Session}
				var p *ServerPublisher
				p, err = NewServerPublisher(s, l.Plane, class, c, store, scope, l.Executor.mailbox, key)
				if err != nil {
					return nil, err
				}
				sp = append(sp, p)
				var receive *RemoteReceiver
				receive, err = NewRemoteReceiver(s, class, c, store, account, scope.User, l.Executor.mailbox, l.Executor.validity, key)
				if err != nil {
					return nil, err
				}
				rr = append(rr, receive)
			}
		}
		anchor := account == r.handoff.origin.Account && l.Plane == Fast
		if r.client {
			if anchor {
				inbox, err = NewClientInboxWithBootstrap(l.Executor, DefaultInboxLimits(), r.handoff, cr...)
			} else {
				inbox, err = NewClientInbox(l.Executor, DefaultInboxLimits(), cr...)
			}
		} else {
			if anchor {
				inbox, err = NewServerInboxWithBootstrap(l.Executor, DefaultInboxLimits(), r.handoff, rr...)
			} else {
				inbox, err = NewServerInbox(l.Executor, DefaultInboxLimits(), rr...)
			}
		}
		if err != nil {
			return nil, err
		}
		if r.client {
			err = inbox.EnableDeliveryReceipts()
		} else {
			err = inbox.EnableReclamation()
		}
		if err != nil {
			return nil, err
		}
		limits := DefaultInboxServiceLimits()
		limits.PreferIdle = true
 if l.Plane==Fast{limits.IdleAudit=limits.MaxProbe}
		reader, err := NewInboxWorker(inbox, limits)
		if err != nil {
			return nil, err
		}
		if r.client {
			writer, err = NewClientPublicationWorker(l.Executor, l.Plane, cp...)
		} else {
			writer, err = NewServerPublicationWorker(l.Executor, l.Plane, sp...)
		}
		if err != nil {
			return nil, err
		}
		if err = svc.Authorities[account].Watch(writer); err != nil {
			return nil, err
		}
		carriers = append(carriers, RuntimeCarrier{Reader: reader, Writer: writer})
	}
	svc.Runtime, err = NewRosterRuntime(r, s, exit, carriers...)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
