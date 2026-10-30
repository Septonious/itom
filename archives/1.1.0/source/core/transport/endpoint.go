package transport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/wallet"
	"itom.local/core/wire"
)

// EndpointOptions describes already selected, budgeted, profile-owned carriers.
// The server borrows its original journals. The client opens fixed local wallet
// paths only after authenticating their issuer. No fixture-issued grants, raw
// Noise keys or substitute accounting files are accepted here.
type EndpointOptions struct {
	// Stage receives fixed nonsecret progress codes on the startup owner.
	Stage func(string)
	// Serialized startup-only observation; it conveys no readiness authority.
	ObservePermission func(RuntimeSnapshot, []AccountPermission)
	// Client startup may share a deadline with its preceding account logins.
	// This never becomes the lifetime of the established runtime.
	StartupDeadline time.Time
	Client          bool
	Template        InvitationTemplate
	Plan            RosterPlan
	Lanes           []RosterLane
	Journals        []RosterJournals
	Wallets         []RosterWalletPaths
	AnchorWallet    string
	Key             []byte
	Service         RosterServiceOptions
}

type fenceRoute struct {
	account string
	class   LedgerClass
}

type EndpointSession struct {
	essentialFences map[fenceRoute]wire.QuotaKey
	Service         *RosterService
	Roster          *RosterResult
	cancel          context.CancelFunc
	done            chan struct{}
	mu              sync.Mutex
	err             error
	anchor          *wallet.Wallet
	requests        map[string]bool
	reconciled      map[wire.QuotaKey]bool
	closeOnce       sync.Once
}

func (s *EndpointSession) Done() <-chan struct{} { return s.done }
func (s *EndpointSession) Err() error            { s.mu.Lock(); defer s.mu.Unlock(); return s.err }
func (s *EndpointSession) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.Service.Runtime.Close()
		<-s.done
		s.Roster.CloseWallets()
		if s.anchor != nil {
			s.anchor.Close()
		}
	})
	return s.Err()
}

// InventoryRetained reads the complete bounded actual UID inventory and every
// full envelope. It deliberately does not refund missing/old mail, close old
// remote grants, delete messages or infer missing wallet spend. Original debt
// remains charged. An incompatible body/profile is an explicit startup error.
func InventoryRetained(ctx context.Context, x *Executor, user string, key []byte) error {
	_, err := InspectRetained(ctx, x, user, key)
	return err
}

func visitBodies(ctx context.Context, x *Executor, visit func(Body) (bool, error)) error {
	return visitBodiesWithStage(ctx, x, visit, func(string) {})
}

func visitBodiesWithStage(ctx context.Context, x *Executor, visit func(Body) (bool, error), stage func(string)) error {
	stage("enumerate")
	command, err := x.enumerateInbox(ctx, false)
	if err != nil {
		return err
	}
	meta, err := command.Wait(ctx)
	if err != nil {
		return err
	}
	for _, m := range meta {
		stage("fetch_body")
		command, err = x.fetchInbox(ctx, m.UID)
		if err != nil {
			return err
		}
		bodies, err := command.Wait(ctx)
		if err != nil {
			return err
		}
		for _, body := range bodies {
			stage("authenticate_body")
			stop, err := visit(body)
			clear(body.Bytes)
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
	}
	return nil
}

func endpointPause(ctx context.Context, x *Executor) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-x.done:
		return ErrClosed
	case <-x.Changes():
	case <-timer.C:
	}
	return nil
}

func findRendezvous(ctx context.Context, x *Executor, o EndpointOptions, store *journal.Store) (*VerifiedRendezvous, error) {
	var found *VerifiedRendezvous
	err := visitBodies(ctx, x, func(body Body) (bool, error) {
		var v *VerifiedRendezvous
		var err error
		if store == nil {
			v, err = VerifyRendezvous(x, body, o.Template, o.Key)
		} else {
			v, err = ObserveRendezvous(store, x, body, o.Template, o.Key)
		}
		if err == nil && (found == nil || v.uid > found.uid) {
			found = v
		}
		// A non-rendezvous BODY is not authority. Retained inventory is checked
		// separately; scanning must not mistake old DATA for a startup offer.
		return false, nil
	})
	return found, err
}

// StartEndpoint runs production rendezvous, actual initial CONTROL allocation,
// Noise confirmation and per-account roster negotiation. It then starts the
// same Runtime used by source application checks and waits for usable CONTROL
// and DATA grants across the selected accounts. Cancellation closes/join owned carriers;
// borrowed journals and original wallet files are never removed.
func StartEndpoint(parent context.Context, o EndpointOptions) (result *EndpointSession, err error) {
	stage := func(code string) {
		if o.Stage != nil {
			o.Stage(code)
		}
	}
	if len(o.Key) != 32 || len(o.Plan.Accounts) == 0 || len(o.Lanes) != 2*len(o.Plan.Accounts) || o.Template.Binding.Session != (wire.ID{}) || o.Template.Binding.Handshake != (wire.ID{}) {
		return nil, ErrProtocol
	}
	plan, _, err := canonicalRosterPlan(o.Plan)
	if err != nil {
		return nil, err
	}
	o.Plan = plan
	account := plan.Accounts[0].Account
	var controlPaths map[string]string
	if o.Client {
		controlPaths, err = endpointControlPaths(o)
	} else {
		account, err = endpointServerAnchor(o)
	}
	if err != nil {
		return nil, err
	}
	var x *Executor
	seen := map[*Executor]bool{}
	scopes := map[string]map[Plane]bool{}
	planned := map[string]RosterAccount{}
	for _, a := range plan.Accounts {
		planned[a.Account] = a
	}
	for _, l := range o.Lanes {
		if l.Executor == nil || seen[l.Executor] {
			return nil, ErrProtocol
		}
		seen[l.Executor] = true
		a, ok := planned[l.Executor.account]
		if !ok || l.Executor.validity == 0 || l.Plane != Fast && l.Plane != Bulk {
			return nil, ErrProtocol
		}
		folder := a.FastFolder
		if l.Plane == Bulk {
			folder = a.BulkFolder
		}
		if l.Executor.mailbox != folder || scopes[a.Account][l.Plane] {
			return nil, ErrProtocol
		}
		if scopes[a.Account] == nil {
			scopes[a.Account] = map[Plane]bool{}
		}
		scopes[a.Account][l.Plane] = true
		if l.Executor.account == account && l.Plane == Fast {
			x = l.Executor
		}
	}
	if x == nil || !x.keywords || !x.uidPlus {
		return nil, ErrProtocol
	}
	ctx, cancel := context.WithCancel(parent)
	startup := ctx
	if o.Client {
		deadline := time.Now().Add(90 * time.Second)
		if !o.StartupDeadline.IsZero() && o.StartupDeadline.Before(deadline) {
			deadline = o.StartupDeadline
		}
		var stop context.CancelFunc
		startup, stop = context.WithDeadline(ctx, deadline)
		defer stop()
	}
	var anchor *wallet.Wallet
	var standby = startStandby(ctx, cancel, o.Lanes, x)
	var roster *RosterResult
	defer func() {
		standby.requestStop()
		standbyFailure := standby.failure()
		if result == nil {
			cancel()
			for _, l := range o.Lanes {
				if l.Executor != nil {
					l.Executor.Close()
				}
			}
			if roster != nil {
				roster.CloseWallets()
			}
			if anchor != nil {
				anchor.Close()
			}
		}
		<-standby.done
		if result == nil && parent.Err() == nil && standbyFailure != nil {
			err = standbyFailure
		}
	}()
	var owner *SessionBootstrap
	var rendezvous *VerifiedRendezvous
	var request string
	requests := map[string]bool{}
	stage("rendezvous")
	if o.Client {
		candidate, e := discoverEndpoint(startup, o, controlPaths, stage)
		if e != nil {
			return nil, e
		}
		anchor, owner, rendezvous, x = candidate.w, candidate.owner, candidate.rv, candidate.x
		account = x.account
		if err = standby.stop(startup); err != nil {
			owner.Close()
			return nil, err
		}
		// Detached discovery commands drain before ownership transfer. No
		// reader, request or NOOP from another discovery owner survives.
		for _, l := range o.Lanes {
			if err = l.Executor.lockWriter(startup); err != nil {
				owner.Close()
				return nil, err
			}
			err = l.Executor.drainCommands(startup)
			<-l.Executor.writer
			if err != nil {
				owner.Close()
				return nil, err
			}
		}
		standby = startStandby(ctx, cancel, o.Lanes, x)
		o.Wallets = append([]RosterWalletPaths(nil), o.Wallets...)
		for n := range o.Wallets {
			p := &o.Wallets[n]
			p.Control = controlPaths[p.Account]
			if p.Account == account {
				p.Control = ""
			}
		}
	} else {
		var store *journal.Store
		for _, j := range o.Journals {
			if j.Account == account {
				store = j.Control
			}
		}
		if store == nil {
			return nil, ErrProtocol
		}
		rendezvous, err = findRendezvous(startup, x, o, store)
		if err != nil {
			return nil, err
		}
		if rendezvous == nil {
			p, e := PrepareRendezvous(startup, store, o.Template, account, x.mailbox, x.validity, o.Key)
			if e != nil {
				return nil, e
			}
			if outcome, e := p.Start(startup, x); e != nil || outcome != Accepted {
				return nil, errors.Join(ErrUnknown, e)
			}
			rendezvous, err = findRendezvous(startup, x, o, store)
		}
		if err != nil || rendezvous == nil {
			return nil, errors.Join(ErrProtocol, err)
		}
		stage("waiting_request")
		rejectedRequest, claimedRequest := false, false
		for owner == nil {
			cmd, e := x.fetchMaintenance(startup, []uint32{rendezvous.uid}, true)
			if e != nil {
				return nil, e
			}
			rows, e := cmd.Wait(startup)
			if e != nil {
				return nil, e
			}
			var flags []string
			for _, row := range rows {
				flags = append(flags, row.Flags...)
			}
			observed := rendezvous.requests(flags, o.Key)
			for _, hint := range observed {
				requests[hint.keyword] = true
			}
			for _, flag := range flags {
				if !rejectedRequest && len(flag) >= 6 && strings.EqualFold(flag[:6], "ITOM2_") {
					if _, e = rendezvous.RequestTemplate(flag, o.Key); e != nil {
						stage("request_rejected")
						rejectedRequest = true
					}
				}
			}
			for _, hint := range observed {
				flag := hint.keyword
				allowance, e := endpointInitialAllowance(store, account, o.Template.Binding.User)
				if e != nil {
					return nil, e
				}
				p, e := rendezvous.PrepareRequestedInvitation(startup, flag, allowance, o.Key)
				if errors.Is(e, journal.ErrState) {
					if !claimedRequest {
						stage("request_already_claimed")
						claimedRequest = true
					}
					if e = rendezvous.clearRequest(startup, x, hint); e != nil && !errors.Is(e, ErrRefused) {
						return nil, e
					}
					continue // original session already claimed; metadata is not authority
				}
				if e != nil {
					return nil, e
				}
				if outcome, e := p.Start(startup, x); e != nil || outcome != Accepted {
					return nil, errors.Join(ErrUnknown, e)
				}
				stage("invitation_published")
				err = visitBodies(startup, x, func(body Body) (bool, error) {
					v, e := rendezvous.ObserveInvitation(x, body, flag, o.Key)
					if e != nil {
						return false, nil
					}
					owner, e = v.NewServer(store, o.Key)
					return true, e
				})
				if err != nil {
					return nil, err
				}
				request = flag
				if owner != nil {
					activeHandshake, stopHandshake := context.WithTimeout(ctx, 90*time.Second)
					defer stopHandshake()
					startup = activeHandshake
					// Remove only this fully authenticated, durably claimed hint.
					// This bounds keyword accumulation across VPN restarts and
					// neither deletes mail nor refunds any original quota.
					if e = rendezvous.clearRequest(startup, x, hint); e != nil && !errors.Is(e, ErrRefused) {
						return nil, e
					}
					break
				}
			}
			if owner == nil {
				if err = endpointPause(startup, x); err != nil {
					return nil, err
				}
			}
		}
	}
	defer owner.Close()
	if o.Client {
		// Start every account with the already accepted request representation.
		// A stricter account can still fall back on its own explicit refusals.
		for _, lane := range o.Lanes {
			lane.Executor.rememberKeywordBytes(x.preferredKeywordBytes())
		}
	}
	installHistory(o.Lanes, owner.binding.User, owner.binding.Session, o.Key)
	stage("noise")
	driver, err := NewBootstrapDriver(owner, x, DefaultInboxServiceLimits())
	if err != nil {
		return nil, err
	}
	h, err := driver.Run(startup)
	if err != nil {
		driver.Close()
		return nil, fmt.Errorf("Noise confirmation: %w", err)
	}
	var rd *RosterDriver
	stage("roster")
	if o.Client {
		rd, err = NewClientAccountRoster(h, plan, o.Lanes, anchor, o.Wallets, o.Key)
	} else {
		rd, err = NewServerAccountRoster(h, plan, o.Lanes, o.Journals, o.Key)
	}
	if err != nil {
		return nil, err
	}
	roster, err = rd.Run(startup)
	if err != nil {
		rd.Close()
		return nil, fmt.Errorf("roster: %w", err)
	}
	if err = standby.stop(startup); err != nil {
		return nil, err
	}
	session, err := NewSession(o.Template.Generation, DefaultLimits())
	if err != nil {
		return nil, err
	}
	// Protect the first actual request, not only requests that have already
	// queued DATA. Surviving wallets may immediately send exact final-spend
	// fences; speculative old re-offers wait for the original short lifetime.
	session.lastForeground = time.Now()
	o.Service.ReconcileHistory = true
	stage("runtime")
	service, err := NewRosterService(roster, session, o.Key, o.Service)
	if err != nil {
		return nil, err
	}
	s := &EndpointSession{Service: service, Roster: roster, cancel: cancel, done: make(chan struct{}), anchor: anchor, requests: requests, reconciled: map[wire.QuotaKey]bool{}}
	go s.run(ctx, o, rendezvous, request)
	if o.Client {
		stage("permission")
		var observed time.Time
		for {
			permissions, ready, e := endpointPermissions(roster)
			if e != nil {
				s.Close()
				return nil, e
			}
			if o.ObservePermission != nil && (observed.IsZero() || ready || time.Since(observed) >= time.Second) {
				o.ObservePermission(service.Runtime.Snapshot(), permissions)
				observed = time.Now()
			}
			if ready {
				break
			}
			select {
			case <-startup.Done():
				s.Close()
				return nil, startup.Err()
			case <-s.done:
				s.Close()
				return nil, errors.Join(ErrRuntimeStopped, s.Err())
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	result = s
	stage("ready")
	return result, nil
}

type AccountPermission struct {
	Account                                 string
	ControlUsable, DataUsable               bool
	CurrentControlOffers, CurrentDataOffers int
}

func endpointPermissions(r *RosterResult) ([]AccountPermission, bool, error) {
	// Storage authority is account-scoped; logical control and DATA can use
	// different healthy accounts. An exhausted account must not hold every
	// independent lane hostage. Actual duplex DATA is checked before VPN READY.
	var control, data bool
	var state []AccountPermission
	for _, a := range r.manifest.Accounts {
		ws := r.wallets[a.Account]
		p := AccountPermission{Account: a.Account}
		for n, w := range []*wallet.Wallet{ws.Control, ws.Data} {
			entries, err := w.Entries()
			if err != nil {
				return nil, false, err
			}
			for _, e := range entries {
				if e.Offer.Key.Session != w.Session() {
					continue
				}
				if n == 0 {
					p.CurrentControlOffers++
				} else {
					p.CurrentDataOffers++
				}
				if !e.Fenced && e.Used.Messages < e.Offer.Limit.Messages && e.Used.Bytes < e.Offer.Limit.Bytes {
					if n == 0 {
						control = true
						p.ControlUsable = true
					} else {
						data = true
						p.DataUsable = true
					}
				}
			}
		}
		state = append(state, p)
	}
	return state, control && data, nil
}

// Two overlapping grants share only the capacity LEFT AFTER historical debt.
// On a clean store this is the original allowance. Existing sessions' held
// reservations are never treated as free because a new namespace/wallet exists.
// Current-session grants are excluded from historical debt so refill does not
// shrink its target every time it issues a grant. The journal remains the final
// atomic admission authority; no limit, balance, or old grant is changed here.
func endpointAllowance(store *journal.Store, account, user string, current wire.ID) (pool.Amount, error) {
	limit, err := store.Limit(account, user)
	if err != nil {
		return pool.Amount{}, err
	}
	usage, err := store.UsageByOrigin(account, user, current)
	if err != nil {
		return pool.Amount{}, err
	}
	bytes := usage.HistoricalLocal.Bytes + usage.HistoricalRemote.Bytes
	messages := usage.HistoricalLocal.Messages + usage.HistoricalRemote.Messages
	if bytes >= limit.Bytes || messages >= limit.Messages {
		return pool.Amount{}, ErrCapacity
	}
	allowance := pool.Amount{Bytes: (limit.Bytes - bytes) / 4, Messages: min(uint64(48), (limit.Messages-messages)/4)}
	if allowance.Bytes == 0 || allowance.Messages == 0 {
		return pool.Amount{}, ErrCapacity
	}
	return allowance, nil
}

func (s *EndpointSession) run(ctx context.Context, o EndpointOptions, rv *VerifiedRendezvous, request string) {
	defer close(s.done)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- s.Service.Runtime.Run(runCtx) }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var policyErr error
	sequences := map[string]map[LedgerClass]uint64{}
	monitor := 0
	for policyErr == nil && runCtx.Err() == nil {
		select {
		case policyErr = <-runtimeDone:
			// Runtime already joined its workers.
			s.mu.Lock()
			s.err = policyErr
			s.mu.Unlock()
			return
		case <-runCtx.Done():
		case <-ticker.C:
			runtime := s.Service.Runtime
			runtime.mu.Lock()
			failed := false
			for _, carrier := range runtime.status {
				if carrier.Read == ServiceFailed || carrier.Write == ServiceFailed {
					failed = true
				}
			}
			runtime.mu.Unlock()
			if failed {
				policyErr = ErrRuntimeStopped
				continue
			}
			if o.Client {
				policyErr = s.clientPolicy(runCtx)
			} else {
				policyErr = s.serverPolicy(runCtx, sequences)
			}
			monitor++
			if policyErr == nil && !o.Client && monitor%10 == 0 {
				policyErr = s.restartRequest(runCtx, rv, request, o.Key)
			}
		}
	}
	cancel()
	s.Service.Runtime.Close()
	runtimeErr := <-runtimeDone
	if policyErr == nil {
		policyErr = runtimeErr
	}
	s.mu.Lock()
	s.err = policyErr
	s.mu.Unlock()
}

var ErrEndpointRestart = errors.New("V2_AUTHENTICATED_VPN_RESTART")

func (s *EndpointSession) restartRequest(ctx context.Context, rv *VerifiedRendezvous, current string, key []byte) error {
	var x *Executor
	for _, c := range s.Service.Runtime.carriers {
		if c.Writer.executor.account == rv.origin.Account && c.Writer.plane == Fast {
			x = c.Writer.executor
		}
	}
	if x == nil {
		return ErrProtocol
	}
	cmd, err := x.fetchMaintenance(ctx, []uint32{rv.uid}, true)
	if err != nil {
		return err
	}
	rows, err := cmd.Wait(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		for _, flag := range rv.RequestKeywords(row.Flags, key) {
			if !strings.EqualFold(flag, current) && !s.requests[flag] {
				return ErrEndpointRestart
			}
		}
	}
	return nil
}

func (s *EndpointSession) serverPolicy(ctx context.Context, sequences map[string]map[LedgerClass]uint64) error {
	// Confirmed startup mail uses its original proof machinery before new offers
	// consume the headroom required to reclaim it. Receipt metadata needs no grant.
	for _, carrier := range s.Service.Runtime.carriers {
		if carrier.Reader.inbox.startupCleanupPending() {
			return nil
		}
	}

	return s.serverPolicyFor(ctx, sequences, s.Roster.handoff.origin.User, s.Roster.handoff.epoch.Context().Session)
}

// Fresh permissions take priority over historical re-offers. The latter still
// travel through any healthy physical CONTROL carrier, even when their own
// ledger is full. Neither queuing metadata nor missing client history refunds
// any reserved or unknown publication debt.
func (s *EndpointSession) serverPolicyFor(ctx context.Context, sequences map[string]map[LedgerClass]uint64, user string, current wire.ID) error {
	type recoveryOffer struct {
		owner *AuthorityController
		class LedgerClass
		offer wire.StorageGrant
	}
	var recovery []recoveryOffer
	for account, j := range s.Roster.journals {
		if sequences[account] == nil {
			sequences[account] = map[LedgerClass]uint64{}
		}
		for _, entry := range []struct {
			class LedgerClass
			store *journal.Store
		}{{ControlLedger, j.Control}, {DataLedger, j.Data}} {
			allowance, allowanceErr := endpointAllowance(entry.store, account, user, current)
			if allowanceErr != nil && !errors.Is(allowanceErr, ErrCapacity) {
				return allowanceErr
			}
			// MaxGrants bounds the live inventory to 256 rows. Exhaustion of
			// physical capacity must not suppress recovery of existing grants.
			grants, err := entry.store.ScanGrants(0, 256)
			if err != nil {
				return err
			}
			owner := s.Service.Authorities[account]
			open := 0
			var remaining pool.Amount
			for _, g := range grants {
				if g.Remote && !g.Closed && g.Ref.Scope.Session != current {
					key := wire.QuotaKey{Store: g.Ref.Store, Epoch: g.Run, Grant: g.Ref.ID, Session: g.Ref.Scope.Session, Account: g.Ref.Scope.Account, User: g.Ref.Scope.User}
					if !s.reconciled[key] && len(recovery) < maxAuthorityQueued {
						offer := wire.StorageGrant{Key: key, Limit: wire.Quota{Bytes: g.Limit.Bytes, Messages: g.Limit.Messages}}
						recovery = append(recovery, recoveryOffer{owner, entry.class, offer})
					}
				}
				if g.Remote && !g.Closed && g.Run == entry.store.Run() && g.Ref.Scope.Session == current {
					open++
					remaining.Bytes += g.Limit.Bytes - g.Used.Bytes
					remaining.Messages += g.Limit.Messages - g.Used.Messages
				}
			}
			owner.mu.Lock()
			prior := owner.issues[entry.class]
			owner.mu.Unlock()
			pending := prior.request != 0 && !prior.queued
			if !pending && (allowanceErr != nil || open >= 2 || remaining.Bytes > allowance.Bytes/2 && remaining.Messages > allowance.Messages/2) {
				continue
			}
			next := sequences[account][entry.class] + 1
			if pending {
				// A reservation that has not entered the bounded queue is the
				// SAME issue. Recovery may change available quota in between;
				// changing this request's amount would break idempotency.
				next, allowance = prior.request, prior.limit
			}
			_, err = owner.Issue(ctx, entry.class, next, allowance)
			if err == nil {
				sequences[account][entry.class] = next
			} else if !errors.Is(err, journal.ErrLimit) && !errors.Is(err, ErrCapacity) {
				return err
			}
		}
	}
	if !s.Service.historicalLaunchAllowed() {
		return nil
	}
	for _, r := range recovery {
		if err := r.owner.queue(r.class, r.offer); err != nil {
			if errors.Is(err, ErrCapacity) {
				break
			}
			return err
		}
		s.reconciled[r.offer.Key] = true
	}
	return nil
}

func (s *EndpointSession) clientPolicy(ctx context.Context) error {
	for account, ws := range s.Roster.wallets {
		for _, entry := range []struct {
			class LedgerClass
			w     *wallet.Wallet
		}{{ControlLedger, ws.Control}, {DataLedger, ws.Data}} {
			rows, err := entry.w.Entries()
			if err != nil {
				return err
			}
			historyAllowed := s.Service.historicalLaunchAllowed()
			route := fenceRoute{account, entry.class}
			urgent := wire.QuotaKey{}
			if !historyAllowed && s.essentialFences[route] == (wire.QuotaKey{}) {
				urgent = largestUnusedHistoricalFence(rows, entry.w.Session())
			}
			usable := 0
			for _, e := range rows {
				if e.Offer.Key.Session == entry.w.Session() && !e.Fenced && e.Used.Messages < e.Offer.Limit.Messages && e.Used.Bytes < e.Offer.Limit.Bytes {
					usable++
				}
			}
			for _, e := range rows {
				if e.Offer.Key.Session != entry.w.Session() {
					// Returning a surviving wallet's exact final unused
					// reservation is capacity progress, not speculative history.
					// The existing bounded authority queue still limits it.
					// OpenBound fenced this exact persisted spend before the new
					// epoch. Only unused allowance is released, never BODY debt.
					err = nil
					if !e.IssuerClosed && !e.Retired && (historyAllowed || e.Offer.Key == urgent) {
						err = s.Service.Authorities[account].Fence(ctx, entry.class, e.Offer.Key)
						if err == nil && !historyAllowed {
							if s.essentialFences == nil {
								s.essentialFences = map[fenceRoute]wire.QuotaKey{}
							}
							s.essentialFences[route] = e.Offer.Key
						}
					}
					if err == nil && e.Retired && historyAllowed {
						err = s.Service.Authorities[account].Retire(entry.class, e.Offer.Key)
					}
					if err != nil && !errors.Is(err, ErrCapacity) && !errors.Is(err, wallet.ErrState) {
						return err
					}
					continue
				}
				// A policy tick may see BOTH overlapping grants already spent.
				// Their final fence is still required when usable == 0;
				// otherwise the issuer keeps two open grants forever.
				exhausted := e.Used.Messages == e.Offer.Limit.Messages || e.Used.Bytes == e.Offer.Limit.Bytes
				if !e.Fenced && (exhausted || usable >= 2 && e.Used.Messages > 0 && (e.Used.Messages >= e.Offer.Limit.Messages/2 || e.Used.Bytes >= e.Offer.Limit.Bytes/2)) {
					err = s.Service.Authorities[account].Fence(ctx, entry.class, e.Offer.Key)
					if err == nil && !exhausted {
						usable--
					}
				} else if e.Fenced && e.Ack == e.Used {
					err = s.Service.Authorities[account].Retire(entry.class, e.Offer.Key)
				}
				if err != nil && !errors.Is(err, ErrCapacity) && !errors.Is(err, wallet.ErrState) {
					return err
				}
			}
		}
	}
	return nil
}

// During short foreground work, return at most one surviving reservation per
// account/ledger. Prefer the largest unused message allowance (then bytes and
// newest grant). A field-sized old wallet must not become a new control flood.
// Quiet/bulk or actual all-account pressure restores the original bounded work.
func largestUnusedHistoricalFence(rows []wallet.Entry, current wire.ID) wire.QuotaKey {
	var key wire.QuotaKey
	var messages, bytes uint64
	for _, entry := range rows {
		if entry.Offer.Key.Session == current || entry.IssuerClosed || entry.Retired {
			continue
		}
		m := entry.Offer.Limit.Messages - entry.Used.Messages
		b := entry.Offer.Limit.Bytes - entry.Used.Bytes
		if m == 0 || b == 0 {
			continue
		}
		if key == (wire.QuotaKey{}) || m > messages || m == messages && (b > bytes || b == bytes && entry.Offer.Key.Grant > key.Grant) {
			key, messages, bytes = entry.Offer.Key, m, b
		}
	}
	return key
}
