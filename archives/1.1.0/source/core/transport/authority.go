package transport

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// ClientAuthorityLedger is supplied by the authenticated startup/roster owner.
// Run is the ORIGINAL issuer Run, not the Noise epoch. No new identity or empty
// wallet is inferred here from an unauthenticated message or missing history.
type ClientAuthorityLedger struct {
	Class  LedgerClass
	Wallet *wallet.Wallet
	Run    uint64
}
type ServerAuthorityLedger struct {
	Class LedgerClass
	Store *journal.Store
}
type authorityRoute struct {
	store  *journal.Store
	wallet *wallet.Wallet
	id     wire.ID
	run    uint64
}
type authorityIssue struct {
	request uint64
	limit   pool.Amount
	ref     journal.GrantRef
	queued  bool
	err     error
}

// AuthorityController serializes durable quota operations outside Session.mu.
// Its immutable routes are also used for PURE semantic validation before the
// Noise replay window advances. There is no automatic grant minting on a timer,
// blind resend, missing-history refund, or production READY claim.
type AuthorityController struct {
	mu            sync.Mutex
	session       *Session
	client        bool
	account, user string
	sessionID     wire.ID
	routes        map[LedgerClass]authorityRoute
	watchers      []*PublicationWorker
	issues        map[LedgerClass]authorityIssue
	counters      [3]authorityCounters
	fences        map[wire.QuotaKey]bool
	retiring      map[wire.QuotaKey]bool
}

func NewClientAuthority(s *Session, c *Cipher, account string, ledgers ...ClientAuthorityLedger) (*AuthorityController, error) {
	if s == nil || c == nil || c.epoch == nil || account == "" || len(ledgers) != 2 {
		return nil, ErrProtocol
	}
	b := c.epoch.Context()
	a := &AuthorityController{session: s, client: true, account: account, user: b.User, sessionID: b.Session, routes: map[LedgerClass]authorityRoute{}}
	ids := map[wire.ID]bool{}
	for _, l := range ledgers {
		if l.Class != DataLedger && l.Class != ControlLedger || l.Wallet == nil || l.Run == 0 {
			return nil, ErrProtocol
		}
		id := l.Wallet.Identity()
		if id.User != b.User || id.Session != b.Session || ids[id.Store] || a.routes[l.Class].wallet != nil {
			return nil, ErrProtocol
		}
		ids[id.Store] = true
		a.routes[l.Class] = authorityRoute{wallet: l.Wallet, id: id.Store, run: l.Run}
	}
	return attachAuthority(a, c, wire.C2S)
}
func NewServerAuthority(s *Session, c *Cipher, account string, ledgers ...ServerAuthorityLedger) (*AuthorityController, error) {
	if s == nil || c == nil || c.epoch == nil || account == "" || len(ledgers) != 2 {
		return nil, ErrProtocol
	}
	b := c.epoch.Context()
	a := &AuthorityController{session: s, account: account, user: b.User, sessionID: b.Session, routes: map[LedgerClass]authorityRoute{}, issues: map[LedgerClass]authorityIssue{}}
	ids := map[wire.ID]bool{}
	for _, l := range ledgers {
		if l.Class != DataLedger && l.Class != ControlLedger || l.Store == nil {
			return nil, ErrProtocol
		}
		if _, err := l.Store.Limit(account, b.User); err != nil {
			return nil, ErrProtocol
		}
		id := l.Store.StoreID()
		if ids[id] || a.routes[l.Class].store != nil {
			return nil, ErrProtocol
		}
		ids[id] = true
		a.routes[l.Class] = authorityRoute{store: l.Store, id: id, run: l.Store.Run()}
	}
	return attachAuthority(a, c, wire.S2C)
}
func attachAuthority(a *AuthorityController, c *Cipher, direction wire.Direction) (*AuthorityController, error) {
	s := a.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.authorities[a.account] != nil || len(s.authorities) >= maxRosterAccounts || s.runtime != nil || s.services != 0 {
		return nil, ErrProtocol
	}
	if err := s.bindCipherLocked(c, direction); err != nil {
		return nil, err
	}
	// Registration publishes a fresh immutable routing view. Receive admission
	// captures it once without copying every account on every encrypted body.
	next := make(map[string]*AuthorityController, len(s.authorities)+1)
	for account, owner := range s.authorities {
		next[account] = owner
	}
	next[a.account] = a
	s.authorities = next
	return a, nil
}

// validate never locks a mutable controller, reads a DB or changes authority.
func (a *AuthorityController) validate(payload []byte, inbound bool) error {
	class, message, err := decodeAuthority(payload)
	if err != nil {
		return err
	}
	route, ok := a.routes[class]
	k := authorityKey(message)
	if !ok || k.Store != route.id || k.Epoch != route.run || k.Session != a.sessionID || k.User != a.user || k.Account != a.account {
		return ErrProtocol
	}
	serverOrigin := a.client == inbound
	switch m := message.(type) {
	case wire.StorageGrant, wire.CleanupStatus:
		if !serverOrigin {
			return ErrProtocol
		}
	case wire.UsageReport, wire.StorageRelease:
		if serverOrigin {
			return ErrProtocol
		}
	case wire.Retirement:
		if m.Ack != serverOrigin {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	return nil
}
func (a *AuthorityController) queue(class LedgerClass, message wire.Control) error {
	payload, err := encodeAuthority(class, message)
	if err != nil {
		return err
	}
	if err := a.validate(payload, false); err != nil {
		return err
	}
	return a.session.queueAuthority(class, message)
}
func authorityRef(k wire.QuotaKey) journal.GrantRef {
	return journal.GrantRef{Store: k.Store, ID: k.Grant, Scope: journal.Scope{Account: k.Account, User: k.User, Session: k.Session}}
}

// Watch connects publication admission to ACTUAL committed quota changes. It
// does not create a permit. DATA/CONTROL and the physical account stay pinned.
func (a *AuthorityController) Watch(w *PublicationWorker) error {
	if w == nil || w.session != a.session || w.executor.account != a.account {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, source := range w.sources {
		if a.routes[source.class].id != source.store {
			return ErrProtocol
		}
	}
	for _, old := range a.watchers {
		if old == w {
			return nil
		}
	}
	if len(a.watchers) >= 128 {
		return ErrCapacity
	}
	a.watchers = append(a.watchers, w)
	w.NotifyPermission()
	return nil
}
func (a *AuthorityController) wake(class LedgerClass) {
	for _, w := range a.watchers {
		for _, source := range w.sources {
			if source.class == class {
				w.NotifyPermission()
				break
			}
		}
	}
}

// Issue is an explicit server-policy action, not a peer-controlled request.
// Sequence starts at 1 per ledger. Repeating that sequence cannot allocate a
// second grant, even if queuing fails. A lost journal commit ACK poisons the
// original store and is never converted into a fresh Issue attempt.
func (a *AuthorityController) Issue(ctx context.Context, class LedgerClass, request uint64, limit pool.Amount) (journal.GrantRef, error) {
	if a.client || request == 0 || ctx.Err() != nil {
		if err := ctx.Err(); err != nil {
			return journal.GrantRef{}, err
		}
		return journal.GrantRef{}, ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	route, ok := a.routes[class]
	if !ok {
		return journal.GrantRef{}, ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		return journal.GrantRef{}, err
	}
	prior := a.issues[class]
	if request < prior.request || request > prior.request+1 || request == prior.request && prior.limit != limit || request > prior.request && prior.request != 0 && !prior.queued {
		return journal.GrantRef{}, ErrProtocol
	}
	if request != prior.request {
		prior = authorityIssue{request: request, limit: limit}
	}
	if prior.queued {
		return prior.ref, nil
	}
	if prior.err != nil && !errors.Is(prior.err, journal.ErrLimit) && !errors.Is(prior.err, journal.ErrRecovery) {
		return prior.ref, prior.err
	}
	if prior.ref.ID == 0 {
		grant, err := route.store.IssueRemote(journal.Scope{Account: a.account, User: a.user, Session: a.sessionID}, limit)
		prior.ref, prior.err = grant.Ref, err
		a.issues[class] = prior
		if err != nil {
			return prior.ref, err
		}
	}
	if err := ctx.Err(); err != nil {
		a.issues[class] = prior
		return prior.ref, err
	}
	offer, err := route.store.RemoteOffer(prior.ref)
	if err == nil {
		err = a.queue(class, offer)
	}
	if err == nil {
		prior.queued = true
	}
	a.issues[class] = prior
	return prior.ref, err
}

// Fence records the ORIGINAL final lifetime spend before queuing a release.
// CONTROL transmission still needs another usable original CONTROL permit;
// fencing a grant cannot create recovery capacity or reset its Used counter.
func (a *AuthorityController) Fence(ctx context.Context, class LedgerClass, key wire.QuotaKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !a.client {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	payload, err := encodeAuthority(class, wire.StorageRelease{Key: key})
	if err != nil || a.validate(payload, false) != nil {
		return ErrProtocol
	}
	if a.fences[key] {
		return nil
	}
	release, err := a.routes[class].wallet.FenceAfterAdmissions(ctx, key)
	if err != nil {
		a.wake(class)
		return err
	}
	if err := ctx.Err(); err != nil {
		a.wake(class)
		return err
	}
	if err := a.queue(class, release); err != nil {
		return err
	}
	if a.fences == nil {
		a.fences = make(map[wire.QuotaKey]bool)
	}
	a.fences[key] = true
	return nil
}

// RefreshStatus is invoked after a real durable event/full proof, never as an
// absence/refund substitute. The original journal computes every counter.
func (a *AuthorityController) RefreshStatus(class LedgerClass, ref journal.GrantRef) error {
	if a.client {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	route, ok := a.routes[class]
	if !ok || ref.Store != route.id || ref.Scope.Account != a.account || ref.Scope.User != a.user || ref.Scope.Session != a.sessionID {
		return ErrProtocol
	}
	status, err := route.store.RemoteStatus(ref)
	if err != nil {
		return err
	}
	a.wake(class)
	return a.queue(class, status)
}

func (a *AuthorityController) apply(payload []byte) error {
	if err := a.validate(payload, true); err != nil {
		return err
	}
	class, message, _ := decodeAuthority(payload)
	a.mu.Lock()
	defer a.mu.Unlock()
	route := a.routes[class]
	switch m := message.(type) {
	case wire.StorageGrant:
		accepted, err := route.wallet.AcceptCurrentOffer(m)
		if err != nil {
			if errors.Is(err, wallet.ErrLimit) {
				return ErrCapacity
			}
			return err
		}
		if accepted {
			a.counters[class].offers.Add(1)
			a.wake(class)
		}
	case wire.CleanupStatus:
		if err := route.wallet.Acknowledge(m); err != nil {
			if errors.Is(err, wallet.ErrMissing) {
				return nil
			}
			return err
		}
		a.counters[class].statuses.Add(1)
		a.wake(class)

	case wire.UsageReport, wire.StorageRelease:
		status, err := route.store.ApplyRemote(a.user, route.run, message)
		if err != nil {
			return err
		}
		if _, fenced := message.(wire.StorageRelease); fenced {
			a.counters[class].fences.Add(1)
		}
		a.wake(class)
		return a.queue(class, status)
	case wire.Retirement:
		if a.client {
			if err := route.wallet.CompleteRetirement(m); err != nil {
				return err
			}
			delete(a.fences, m.Key)
			delete(a.retiring, m.Key)
			a.counters[class].retirements.Add(1)
			a.wake(class)
			return nil
		}
		ack, err := route.store.RetireRemote(a.user, route.run, m)
		if errors.Is(err, journal.ErrMissing) {
			ack = m
			ack.Ack = true
			err = nil
		} // metadata absence only; client must ALREADY have durable final cleanup proof
		if err != nil {
			return err
		}
		return a.queue(class, ack)
	}
	return nil
}

// Only the trusted RemoteReceiver's durable result enters this path. Status
// coalesces in undispatched CONTROL work and acknowledges descriptor history,
// never lifetime spend or physical absence. Client-origin status ACKs do not
// require a further application-level ACK of that status.
func (a *AuthorityController) observeStoredStatus(status wire.CleanupStatus) error {
	if a.client {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for class, route := range a.routes {
		if route.id == status.Key.Store {
			return a.queue(class, status)
		}
	}
	return ErrProtocol
}

type authorityCounters struct{ offers, statuses, fences, retirements atomic.Uint64 }

// Counts include successfully handled duplicates; they are causal metadata
// events, not useful traffic, RTT samples or additional quota authority.
type AuthorityStats struct{ AcceptedOffers, AppliedStatuses, AppliedFences, RetirementAcknowledgements uint64 }

func (a *AuthorityController) Stats(class LedgerClass) AuthorityStats {
	if class != DataLedger && class != ControlLedger {
		return AuthorityStats{}
	}
	c := &a.counters[class]
	return AuthorityStats{c.offers.Load(), c.statuses.Load(), c.fences.Load(), c.retirements.Load()}
}

// Retire is an explicit metadata-owner action AFTER the wallet has durably
// acknowledged final cleanup. Keeping it separate lets progress-only status
// handling finish without requiring space for new outgoing protocol work.
func (a *AuthorityController) Retire(class LedgerClass, key wire.QuotaKey) error {
	if !a.client {
		return ErrProtocol
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	payload, err := encodeAuthority(class, wire.Retirement{Key: key})
	if err != nil || a.validate(payload, false) != nil {
		return ErrProtocol
	}
	if a.retiring[key] {
		return nil
	}
	retirement, err := a.routes[class].wallet.Retirement(key)
	if err != nil {
		return err
	}
	if retirement == nil {
		return wallet.ErrState
	}
	if err := a.queue(class, *retirement); err != nil {
		return err
	}
	if a.retiring == nil {
		a.retiring = make(map[wire.QuotaKey]bool)
	}
	a.retiring[key] = true
	return nil
}

// notifyReclaimed follows an ACTUAL successful original deletion-proof commit.
// It wakes local publisher admission only; wallet spend and grants are not
// replenished, and no status/permission is inferred from an EXPUNGE tag alone.
func (s *Session) notifyReclaimed(account string, store wire.ID) {
	s.mu.Lock()
	owner := s.authorities[account]
	s.mu.Unlock()
	if owner == nil {
		return
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	for class, route := range owner.routes {
		if route.id == store {
			owner.wake(class)
		}
	}
}
