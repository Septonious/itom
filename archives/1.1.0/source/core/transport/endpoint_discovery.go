package transport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"itom.local/core/journal"
	"itom.local/core/wallet"
)

// Discovery caches only bodies already fetched under this pinned UIDVALIDITY.
// Repeated inventories still find late lower UIDs; an omitted/missing BODY is
// not cached. This is routing knowledge, never accounting or absence proof.
type endpointScan struct {
	x    *Executor
	seen map[uint32]bool
}

func (s *endpointScan) poll(ctx context.Context, visit func(Body) error) error {
	c, err := s.x.enumerateInbox(ctx, false)
	if err != nil {
		return err
	}
	rows, err := c.Wait(ctx)
	if err != nil {
		return err
	}
	if len(rows) > DefaultInboxLimits().KnownUIDs {
		return ErrCapacity
	}
	present := make(map[uint32]bool, len(rows))
	for _, r := range rows {
		present[r.UID] = true
	}
	for uid := range s.seen {
		if !present[uid] {
			delete(s.seen, uid)
		}
	}
	for _, r := range rows {
		if s.seen[r.UID] {
			continue
		}
		c, err = s.x.fetchInbox(ctx, r.UID)
		if err != nil {
			return err
		}
		bodies, e := c.Wait(ctx)
		if e != nil {
			return e
		}
		for _, b := range bodies {
			e = visit(b)
			clear(b.Bytes)
			if e != nil {
				return e
			}
			if s.seen == nil {
				s.seen = map[uint32]bool{}
			}
			s.seen[b.UID] = true
		}
	}
	return nil
}

// Resolve original on-disk identities before discovery opens or rotates any
// wallet. Moving the bootstrap role never moves, replaces or reuses a file.
func endpointControlPaths(o EndpointOptions) (map[string]string, error) {
	if len(o.Wallets) != len(o.Plan.Accounts) || o.AnchorWallet == "" {
		return nil, ErrProtocol
	}
	paths, seen := map[string]string{}, map[string]bool{}
	for _, p := range o.Wallets {
		if _, ok := paths[p.Account]; ok {
			return nil, ErrProtocol
		}
		control := p.Control
		if p.Account == o.Plan.Accounts[0].Account {
			if control != "" {
				return nil, ErrProtocol
			}
			control = o.AnchorWallet
		}
		if control == "" || p.Data == "" {
			return nil, ErrProtocol
		}
		for _, name := range []string{control, p.Data} {
			absolute, e := filepath.Abs(name)
			if e != nil || seen[absolute] {
				return nil, ErrProtocol
			}
			seen[absolute] = true
		}
		paths[p.Account] = control
	}
	for _, a := range o.Plan.Accounts {
		if paths[a.Account] == "" {
			return nil, ErrProtocol
		}
	}
	return paths, nil
}

// Select remaining original CONTROL headroom, not an account-name constant.
// No reservation, refund, replacement issuer or changed limit is involved.
func endpointServerAnchor(o EndpointOptions) (string, error) {
	var best string
	var messages, bytes uint64
	for _, a := range o.Plan.Accounts {
		var store *journal.Store
		for _, j := range o.Journals {
			if j.Account == a.Account {
				store = j.Control
			}
		}
		if store == nil {
			return "", ErrProtocol
		}
		limit, e := store.Limit(a.Account, o.Template.Binding.User)
		if e != nil {
			return "", e
		}
		held, e := store.Held(a.Account, o.Template.Binding.User)
		if e != nil {
			return "", e
		}
		if held.Messages >= limit.Messages || held.Bytes >= limit.Bytes {
			continue
		}
		m, b := limit.Messages-held.Messages, limit.Bytes-held.Bytes
		if best == "" || m > messages || m == messages && b > bytes {
			best, messages, bytes = a.Account, m, b
		}
	}
	if best == "" {
		return "", journal.ErrLimit
	}
	return best, nil
}

type endpointCandidate struct {
	x     *Executor
	rv    *VerifiedRendezvous
	w     *wallet.Wallet
	owner *SessionBootstrap
	flags []string
	err   error
}

func (c *endpointCandidate) discover(ctx context.Context, o EndpointOptions, path string, progress chan<- string) {
	scan := endpointScan{x: c.x}
	for ctx.Err() == nil {
		latest := c.rv
		var invitation *VerifiedInvitation
		c.err = scan.poll(ctx, func(body Body) error {
			v, e := VerifyRendezvous(c.x, body, o.Template, o.Key)
			if e == nil && (latest == nil || v.uid > latest.uid) {
				latest = v
			}
			if c.rv != nil && c.w != nil {
				v, e := c.rv.VerifyInvitation(c.x, body, c.w, o.Key)
				if e == nil {
					invitation = v
				}
			}
			return nil
		})
		if c.err != nil {
			return
		}
		if latest != nil && (c.rv == nil || latest.uid != c.rv.uid || latest.origin != c.rv.origin) {
			if c.rv != nil && len(c.flags) > 0 {
				c.err = c.clear(ctx)
				if c.err != nil && !errors.Is(c.err, ErrRefused) {
					return
				}
			}
			if c.w != nil {
				c.w.Close()
			}
			c.rv = latest
			c.flags = nil
			c.w, c.err = c.rv.OpenWallet(path)
			if c.err != nil {
				return
			}
			c.flags, c.err = c.rv.submitTracked(ctx, c.x, c.w, o.Key, func(code string) {
				select {
				case progress <- code:
				default:
				}
			})
			if c.err != nil {
				return
			}
			continue
		}
		if invitation != nil {
			c.owner, c.err = invitation.NewClient(c.w, o.Key)
			return
		}
		c.err = endpointPause(ctx, c.x)
		if c.err != nil {
			return
		}
	}
	c.err = ctx.Err()
}
func (c *endpointCandidate) clear(ctx context.Context) error {
	if c.rv == nil || len(c.flags) == 0 {
		return nil
	}
	command, e := c.x.storeKeywords(ctx, c.rv.uid, c.flags, false)
	if e == nil {
		_, e = command.Wait(ctx)
	}
	return e
}

// Probe authentic advertisements on all planned FAST lanes with fresh original
// wallet sessions. Only a full MAC-verified invitation can choose the winner;
// a stale advertisement, UID order across accounts or provider flag cannot.
// Requests are metadata only and create no grant or physical BODY on their own.
func discoverEndpoint(ctx context.Context, o EndpointOptions, paths map[string]string, stage func(string)) (*endpointCandidate, error) {
	search, stop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	var candidates []*endpointCandidate
	results := make(chan *endpointCandidate, len(o.Plan.Accounts))
	progress := make(chan string, 128)
	for _, lane := range o.Lanes {
		if lane.Plane != Fast {
			continue
		}
		c := &endpointCandidate{x: lane.Executor}
		candidates = append(candidates, c)
		workers.Go(func() { c.discover(search, o, paths[c.x.account], progress); results <- c })
	}
	var winner *endpointCandidate
	var failures []error
	left := len(candidates)
	for winner == nil && left > 0 && ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case code := <-progress:
			stage(code)
		case c := <-results:
			left--
			if c.owner != nil && c.err == nil {
				winner = c
			} else if c.err != nil {
				failures = append(failures, c.err)
			}
		}
	}
	stop()
	joined := make(chan struct{})
	go func() { workers.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-ctx.Done():
		for _, l := range o.Lanes {
			l.Executor.Close()
		}
		<-joined
	}
	// Clear only our exact submitted flags. An unused wallet is retained and
	// closed before roster opens the same original file under the chosen epoch.
	for _, c := range candidates {
		if ctx.Err() == nil {
			if e := c.clear(ctx); e != nil && !errors.Is(e, ErrRefused) {
				failures = append(failures, e)
			}
		}
	}
	if ctx.Err() != nil {
		winner = nil
	}
	for _, c := range candidates {
		if c != winner {
			if c.owner != nil {
				c.owner.Close()
			}
			if c.w != nil {
				c.w.Close()
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if winner == nil {
		return nil, errors.Join(append([]error{ErrProtocol}, failures...)...)
	}
	stage(fmt.Sprintf("request_%d", winner.x.preferredKeywordBytes()))
	stage("waiting_invitation")
	return winner, nil
}
