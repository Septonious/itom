package transport

import (
	"context"
	"crypto/sha256"
	"errors"
	"sort"
	"sync"
	"time"

	"itom.local/core/journal"
	"itom.local/core/wallet"
)

// RosterSnapshot separates local command service from peer confirmation. None
// of these timings is an application RTT or permission to report VPN CONNECTED.
type RosterSnapshot struct {
	Phase            ServicePhase     `json:"phase"`
	Client           bool             `json:"client"`
	Failure          string           `json:"failure,omitempty"`
	Prepared         uint64           `json:"prepared"`
	Accepted         uint64           `json:"accepted"`
	Uncertain        uint64           `json:"uncertain"`
	DiscoveryTurns   uint64           `json:"discovery_turns"`
	OwnObserved      int              `json:"own_observed"`
	PeerObserved     int              `json:"peer_observed"`
	SelectedAccounts int              `json:"selected_accounts"`
	RosterConfirmed  bool             `json:"roster_confirmed"`
	PrepareNS        int64            `json:"prepare_ns"`
	AppendNS         int64            `json:"append_ns"`
	DiscoveryNS      int64            `json:"discovery_ns"`
	WaitNS           int64            `json:"wait_ns"`
	ElapsedNS        int64            `json:"elapsed_ns"`
	IMAP             ExecutorSnapshot `json:"imap"`
}

// RosterDriver owns already selected lanes between key establishment and
// runtime construction. It creates no sockets, folders, grants or new identity.
// Its exchange is bounded encrypted OFFER chunks, client ACCEPT and server READY.
// READY requires full actual BODY observation, not just APPEND acceptance.
type RosterDriver struct {
	mu                      sync.Mutex
	input                   *BootstrapHandoff
	lanes                   []RosterLane
	anchor                  *Executor
	plan                    RosterPlan
	planHash                [32]byte
	client                  bool
	wallet                  *wallet.Wallet
	store                   *journal.Store
	dataPath                string
	dataWallet              *wallet.Wallet
	key                     [32]byte
	manifest                RosterManifest
	encoded                 []byte
	digest                  [32]byte
	send, receive           uint8
	observed                [3]bool
	own                     map[[32]byte]uint8
	ownSeen, peer           map[[32]byte]bool
	sentOffset              int
	offerParts              map[int][]byte
	offerDigest             [32]byte
	offerTotal              int
	walletPaths             map[string]RosterWalletPaths
	wallets                 map[string]RosterWallets
	ownedWallets            []*wallet.Wallet
	journals                map[string]RosterJournals
	known                   map[uint32]bool
	pending                 *PreparedPublication
	inbox                   *Inbox
	running, closed, failed bool
	result                  *RosterResult
	status                  RosterSnapshot
}

type RosterResult struct {
	client         bool
	manifest       RosterManifest
	handoff        *BootstrapHandoff
	lanes          []RosterLane
	dataWallet     *wallet.Wallet
	wallets        map[string]RosterWallets
	journals       map[string]RosterJournals
	ownedWallets   []*wallet.Wallet
	walletClose    sync.Once
	walletCloseErr error
	serviceMu      sync.Mutex
	serviceClaimed bool
}

// Manifest returns a detached copy of authenticated metadata. It is not a grant.
func (r *RosterResult) Manifest() RosterManifest {
	m := r.manifest
	m.Accounts = append([]RosterAccountState(nil), m.Accounts...)
	return m
}
func (r *RosterResult) Handoff() *BootstrapHandoff { return r.handoff }

// DataWallet transfers the original client wallet handle; the server has none.
// The lifecycle owner must close it after the runtime, retaining its file.
func (r *RosterResult) DataWallet() *wallet.Wallet { return r.dataWallet }

func NewClientRoster(h *BootstrapHandoff, plan RosterPlan, lanes []RosterLane, w *wallet.Wallet, dataPath string, key []byte) (*RosterDriver, error) {
	if h == nil || h.epoch == nil || !h.client || w == nil || dataPath == "" {
		return nil, ErrProtocol
	}
	id := w.Identity()
	b := h.epoch.Context()
	if id.Store != h.origin.Store || id.User != b.User || id.Session != b.Session {
		return nil, ErrProtocol
	}
	d := &RosterDriver{input: h, client: true, wallet: w, dataPath: dataPath, receive: 1}
	return newRoster(d, plan, lanes, key)
}
func NewServerRoster(h *BootstrapHandoff, plan RosterPlan, lanes []RosterLane, control, data *journal.Store, key []byte) (*RosterDriver, error) {
	var bindings []RosterJournals
	for _, a := range plan.Accounts {
		bindings = append(bindings, RosterJournals{Account: a.Account, Control: control, Data: data})
	}
	return NewServerAccountRoster(h, plan, lanes, bindings, key)
}
func newRoster(d *RosterDriver, plan RosterPlan, lanes []RosterLane, key []byte) (*RosterDriver, error) {
	if len(key) != 32 || sha256.Sum256(key) != d.input.keyHash {
		return nil, ErrProtocol
	}
	plan, hash, err := canonicalRosterPlan(plan)
	if err != nil {
		return nil, err
	}
	if len(lanes) != 2*len(plan.Accounts) {
		return nil, ErrProtocol
	}
	lanes = append([]RosterLane(nil), lanes...)
	seen := map[*Executor]bool{}
	for _, l := range lanes {
		if l.Executor == nil || l.Plane > Bulk || seen[l.Executor] {
			return nil, ErrProtocol
		}
		seen[l.Executor] = true
	}
	sort.Slice(lanes, func(i, j int) bool {
		if lanes[i].Executor.account != lanes[j].Executor.account {
			return lanes[i].Executor.account < lanes[j].Executor.account
		}
		return lanes[i].Plane < lanes[j].Plane
	})
	for n, a := range plan.Accounts {
		fast, bulk := lanes[2*n], lanes[2*n+1]
		if fast.Plane != Fast || bulk.Plane != Bulk || fast.Executor.account != a.Account || bulk.Executor.account != a.Account || fast.Executor.mailbox != a.FastFolder || bulk.Executor.mailbox != a.BulkFolder || fast.Executor.validity == 0 || bulk.Executor.validity == 0 {
			return nil, ErrProtocol
		}
		for _, l := range []RosterLane{fast, bulk} {
			if !l.Executor.keywords {
				return nil, ErrKeywordsUnsupported
			}
			if !d.client && !l.Executor.uidPlus {
				return nil, ErrUIDPlusRequired
			}
		}
		state := RosterAccountState{RosterAccount: a, FastUIDValidity: fast.Executor.validity, BulkUIDValidity: bulk.Executor.validity}
		if !d.client {
			j := d.journals[a.Account]
			state.RosterIssuer = RosterIssuer{ControlStore: j.Control.StoreID(), ControlRun: j.Control.Run(), DataStore: j.Data.StoreID(), DataRun: j.Data.Run()}
		}
		d.manifest.Accounts = append(d.manifest.Accounts, state)
		if a.Account == d.input.origin.Account && a.FastFolder == d.input.origin.Folder && fast.Executor.validity == d.input.origin.UIDValidity {
			d.anchor = fast.Executor
		}
	}
	if d.anchor == nil {
		return nil, ErrProtocol
	}
	d.plan, d.planHash, d.lanes = plan, hash, lanes
	copy(d.key[:], key)
	if !d.client {
		d.encoded, err = encodeRoster(d.manifest, plan, hash)
		if err != nil {
			return nil, err
		}
		d.digest = sha256.Sum256(d.encoded)
	}
	d.own, d.ownSeen, d.peer, d.known = map[[32]byte]uint8{}, map[[32]byte]bool{}, map[[32]byte]bool{}, map[uint32]bool{}
	for _, uid := range d.input.uids {
		d.known[uid] = true
	}
	d.status = RosterSnapshot{Phase: ServicePrepared, Client: d.client, SelectedAccounts: len(plan.Accounts)}
	d.inbox, err = NewInbox(d.anchor, InboxScope{d.input.origin.Account, d.input.origin.Folder, d.input.origin.UIDValidity}, DefaultInboxLimits(), func(_ uint32, body Body) (InboxDelivery, error) {
		return InboxAction(func() error { return d.receiveBody(body) }), nil
	})
	if err != nil {
		return nil, err
	}
	// These physically observed startup UIDs remain known even when a replica
	// temporarily omits them. This is discovery knowledge, never absence proof.
	d.inbox.bootstrapUIDs = make(map[uint32]bool, len(d.known))
	for uid := range d.known {
		d.inbox.seen[uid] = true
		d.inbox.bootstrapUIDs[uid] = true
	}
	d.inbox.stopAfterApply = func() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.readyLocked() }
	d.input.mu.Lock()
	defer d.input.mu.Unlock()
	if d.input.rosterConsumed || d.input.rosterComplete {
		return nil, ErrProtocol
	}
	for _, l := range lanes {
		l.Executor.mu.Lock()
	}
	defer func() {
		for n := len(lanes) - 1; n >= 0; n-- {
			lanes[n].Executor.mu.Unlock()
		}
	}()
	for _, l := range lanes {
		x := l.Executor
		if x.closed != nil {
			return nil, selectedLaneError(l)
		}
		if x.bootstrapOwner || x.publicationOwner || x.inboxOwner || !x.onlyStandbyPendingLocked() {
			return nil, ErrProtocol
		}
	}
	for _, l := range lanes {
		l.Executor.bootstrapOwner = true
	}
	d.input.rosterConsumed = true
	return d, nil
}

func (d *RosterDriver) Snapshot() RosterSnapshot {
	d.mu.Lock()
	s := d.status
	s.OwnObserved = len(d.ownSeen)
	s.PeerObserved = len(d.peer)
	d.mu.Unlock()
	s.IMAP = d.anchor.Snapshot()
	return s
}
func (d *RosterDriver) Pending() *PreparedPublication {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.pending
}
func (d *RosterDriver) readyLocked() bool {
	if d.failed || d.closed {
		return false
	}
	for _, v := range d.observed {
		if !v {
			return false
		}
	}
	if d.client && d.dataWallet == nil {
		return false
	}
	if d.pending != nil {
		d.pending.mu.Lock()
		ready := d.pending.finished && d.pending.outcome == Accepted
		d.pending.mu.Unlock()
		if !ready {
			return false
		}
	}
	return true
}
func (d *RosterDriver) Run(ctx context.Context) (result *RosterResult, err error) {
	d.mu.Lock()
	if d.running || d.closed || d.failed {
		d.mu.Unlock()
		return nil, ErrProtocol
	}
	if d.result != nil {
		r := d.result
		d.mu.Unlock()
		return r, nil
	}
	d.running = true
	d.status.Phase = ServiceServing
	d.status.Failure = ""
	started := time.Now()
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.running = false
		d.status.ElapsedNS += time.Since(started).Nanoseconds()
		if result == nil {
			d.status.Phase = ServiceFailed
			if ctx.Err() != nil {
				d.status.Phase = ServiceStopped
			}
			d.status.Failure = bootstrapFailure(err)
		}
		d.mu.Unlock()
	}()
	limits := DefaultInboxServiceLimits()
	delay := limits.MinProbe
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d.mu.Lock()
		if d.readyLocked() {
			h := &BootstrapHandoff{epoch: d.input.epoch, generation: d.input.generation, client: d.client, origin: d.input.origin, keyHash: d.input.keyHash, rosterComplete: true}
			for uid := range d.known {
				h.uids = append(h.uids, uid)
			}
			r := &RosterResult{client: d.client, manifest: d.manifest, handoff: h, lanes: d.lanes, dataWallet: d.dataWallet, wallets: d.wallets, journals: d.journals, ownedWallets: d.ownedWallets}
			d.result = r
			d.status.Phase = ServiceStopped
			d.status.RosterConfirmed = true
			for _, l := range d.lanes {
				l.Executor.mu.Lock()
				l.Executor.bootstrapOwner = false
				l.Executor.mu.Unlock()
			}
			d.mu.Unlock()
			return r, nil
		}
		d.mu.Unlock()
		space := d.anchor.SpaceChanged()
		prepareBegin := time.Now()
		p, e := d.prepare(ctx)
		d.mu.Lock()
		d.status.PrepareNS += time.Since(prepareBegin).Nanoseconds()
		d.mu.Unlock()
		if e != nil && !errors.Is(e, ErrBootstrapPending) {
			return nil, e
		}
		if p != nil {
			appendBegin := time.Now()
			outcome, e := p.Start(ctx, d.anchor)
			d.mu.Lock()
			d.status.AppendNS += time.Since(appendBegin).Nanoseconds()
			if outcome == Accepted {
				d.status.Accepted++
			}
			if outcome == Uncertain {
				d.status.Uncertain++
			}
			d.mu.Unlock()
			if outcome == Uncertain {
				return nil, errors.Join(ErrUnknown, e)
			}
			if e != nil && !errors.Is(e, errCommandCapacity) {
				return nil, e
			}
			if e == nil && outcome != Accepted {
				return nil, ErrProtocol
			}
		}
		begin := time.Now()
		progress, e := d.inbox.Poll(ctx)
		elapsed := time.Since(begin)
		d.mu.Lock()
		d.status.DiscoveryTurns++
		d.status.DiscoveryNS += elapsed.Nanoseconds()
		d.mu.Unlock()
		if e != nil && !errors.Is(e, errCommandCapacity) {
			return nil, e
		}
		if e == nil && progress.Applied > 0 {
			continue
		}
		delay = max(limits.MinProbe, min(limits.MaxProbe, elapsed/8))
		waitBegin := time.Now()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-d.anchor.done:
			timer.Stop()
			return nil, ErrClosed
		case <-space:
		case <-d.anchor.Changes():
		case <-timer.C:
		}
		timer.Stop()
		d.mu.Lock()
		d.status.WaitNS += time.Since(waitBegin).Nanoseconds()
		d.mu.Unlock()
	}
}
func (d *RosterDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	clear(d.key[:])
	d.status.Phase = ServiceClosed
	if d.result == nil {
		for _, l := range d.lanes {
			l.Executor.Close()
		}
		var err error
		for _, w := range d.ownedWallets {
			err = errors.Join(err, w.Close())
		}
		d.ownedWallets = nil
		return err
	}
	return nil
}

func NewRosterRuntime(result *RosterResult, s *Session, exit *ExitServer, carriers ...RuntimeCarrier) (*Runtime, error) {
	if result == nil || result.handoff == nil || !result.handoff.rosterComplete || result.client != (exit == nil) || len(carriers) != len(result.lanes) {
		return nil, ErrProtocol
	}
	allowed := map[*Executor]Plane{}
	for _, l := range result.lanes {
		allowed[l.Executor] = l.Plane
	}
	for _, c := range carriers {
		if c.Reader == nil || c.Reader.inbox == nil || c.Writer == nil {
			return nil, ErrProtocol
		}
		i := c.Reader.inbox
		plane, ok := allowed[i.executor]
		if !ok || plane != c.Writer.plane {
			return nil, ErrProtocol
		}
		i.receiveMu.Lock()
		entry, ok := result.manifest.Account(i.scope.Account)
		matches := ok && i.routes[DataLedger] == entry.DataStore
		if plane == Fast {
			matches = matches && i.routes[ControlLedger] == entry.ControlStore
		}
		if i.scope == (InboxScope{result.handoff.origin.Account, result.handoff.origin.Folder, result.handoff.origin.UIDValidity}) {
			for _, uid := range result.handoff.uids {
				matches = matches && i.bootstrapUIDs[uid]
			}
		}
		i.receiveMu.Unlock()
		if !matches {
			return nil, ErrProtocol
		}
		delete(allowed, i.executor)
	}
	if len(allowed) != 0 || s == nil {
		return nil, ErrProtocol
	}
	s.mu.Lock()
	same := s.crypto == result.handoff.epoch && s.generation == result.handoff.generation
	s.mu.Unlock()
	if !same {
		return nil, ErrProtocol
	}
	budget := result.manifest.ServerBudget
	if result.client {
		budget = result.manifest.ClientBudget
	}
	return NewRuntime(s, exit, budget, carriers...)
}
