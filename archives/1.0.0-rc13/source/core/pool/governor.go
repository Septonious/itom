// Package pool models one shared account coordinator. Calls must be serialized
// by its actor and all remote events authenticated before entry. This package
// does not persist grants: a durable coordinator must journal BEFORE publishing
// a successful Issue or reusing released capacity. No timeout releases credit.
package pool

import (
	"errors"
	"sort"

	"itom.local/core/wire"
)

var (
	ErrLimit = errors.New("POOL_LIMIT")
	ErrScope = errors.New("GRANT_SCOPE")
	ErrStale = errors.New("GRANT_RETIRED")
	ErrOrder = errors.New("GRANT_COUNTER")
)

type Amount struct{ Bytes, Messages uint64 }

func (a Amount) within(b Amount) bool { return a.Bytes <= b.Bytes && a.Messages <= b.Messages }
func (a Amount) add(b Amount) Amount  { return Amount{a.Bytes + b.Bytes, a.Messages + b.Messages} }
func (a Amount) sub(b Amount) Amount  { return Amount{a.Bytes - b.Bytes, a.Messages - b.Messages} }
func (a Amount) zero() bool           { return a.Bytes == 0 && a.Messages == 0 }

// Limits exclude the separately reserved bootstrap/control budget. Shares are
// protected per user on EVERY account, not an account partition into 5 + 5.
type Account struct {
	ID, Provider string
	DataLimit    Amount
	UserShares   map[string]Amount
}
type Token struct {
	Epoch   uint64
	ID      uint64
	User    string
	Session wire.ID
}
type Grant struct {
	Token                Token
	Account              string
	Limit, Used, Cleaned Amount
	Closed               bool
}
type accountState struct {
	config Account
	held   Amount
	byUser map[string]Amount
}
type Governor struct {
	epoch, next uint64
	maxGrants   int
	accounts    map[string]*accountState
	grants      map[uint64]*Grant
}

func validUser(s string) bool {
	if len(s) != 8 || s[0] == '0' {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func New(epoch uint64, configs []Account, maxGrants int) (*Governor, error) {
	if epoch == 0 || len(configs) == 0 || len(configs) > 64 || maxGrants < 1 || maxGrants > 65536 {
		return nil, ErrLimit
	}
	g := &Governor{epoch: epoch, next: 1, maxGrants: maxGrants, accounts: map[string]*accountState{}, grants: map[uint64]*Grant{}}
	provider := configs[0].Provider
	if provider == "" || len(provider) > 255 {
		return nil, ErrScope
	}
	for _, c := range configs {
		if c.ID == "" || len(c.ID) > 64 || c.Provider != provider || g.accounts[c.ID] != nil ||
			c.DataLimit.Bytes == 0 || c.DataLimit.Bytes > 1<<40 || c.DataLimit.Messages == 0 ||
			c.DataLimit.Messages > 1<<24 || len(c.UserShares) == 0 || len(c.UserShares) > 1024 {
			return nil, ErrLimit
		}
		shares := map[string]Amount{}
		total := Amount{}
		for user, share := range c.UserShares {
			if !validUser(user) || share.Bytes == 0 || share.Messages == 0 || !share.within(c.DataLimit) {
				return nil, ErrScope
			}
			total = total.add(share)
			shares[user] = share
		}
		if !total.within(c.DataLimit) {
			return nil, ErrLimit
		}
		c.UserShares = shares
		g.accounts[c.ID] = &accountState{config: c, byUser: map[string]Amount{}}
	}
	return g, nil
}

func (g *Governor) Issue(user string, session wire.ID, account string, limit Amount) (Grant, error) {
	a := g.accounts[account]
	if a == nil || session == (wire.ID{}) {
		return Grant{}, ErrScope
	}
	share, ok := a.config.UserShares[user]
	if !ok {
		return Grant{}, ErrScope
	}
	if limit.Bytes == 0 || limit.Messages == 0 || !limit.within(a.config.DataLimit.sub(a.held)) ||
		!limit.within(share.sub(a.byUser[user])) || len(g.grants) >= g.maxGrants || g.next == 0 {
		return Grant{}, ErrLimit
	}
	token := Token{Epoch: g.epoch, ID: g.next, User: user, Session: session}
	g.next++
	x := &Grant{Token: token, Account: account, Limit: limit}
	g.grants[token.ID] = x
	a.held = a.held.add(limit)
	a.byUser[user] = a.byUser[user].add(limit)
	return *x, nil
}
func (g *Governor) get(token Token) (*Grant, error) {
	if token.Epoch != g.epoch || token.ID == 0 {
		return nil, ErrScope
	}
	x := g.grants[token.ID]
	if x == nil {
		return nil, ErrStale
	}
	if x.Token != token {
		return nil, ErrScope
	}
	return x, nil
}

// Usage is cumulative. A delayed older update is harmless, but crossed
// counters (bytes grow while message count shrinks) are inconsistent.
func (g *Governor) Usage(token Token, used Amount) error {
	x, err := g.get(token)
	if err != nil {
		return err
	}
	if used.within(x.Used) {
		return nil
	}
	if !x.Used.within(used) || !used.within(x.Limit) || x.Closed {
		return ErrOrder
	}
	x.Used = used
	return nil
}
func (g *Governor) release(x *Grant, amount Amount) {
	a := g.accounts[x.Account]
	a.held = a.held.sub(amount)
	a.byUser[x.Token.User] = a.byUser[x.Token.User].sub(amount)
}

// Close is an authenticated final commitment: the client stopped admitting
// writes under this grant. Used includes every started/uncertain APPEND.
// It releases ONLY the unused portion. Silence, expiry or RECEIPT cannot Close.
func (g *Governor) Close(token Token, final Amount) error {
	x, err := g.get(token)
	if err != nil {
		return err
	}
	if x.Closed {
		if final == x.Used {
			return nil
		}
		return ErrOrder
	}
	if !x.Used.within(final) || !final.within(x.Limit) {
		return ErrOrder
	}
	x.Used = final
	x.Closed = true
	g.release(x, x.Limit.sub(final))
	g.retire(x)
	return nil
}

// Clean is called ONLY by the coordinator after verifying concrete absence /
// deletion of owned messages, never from an unverified client counter. Keeping
// this separate prevents a data receipt from recycling physical quota.
func (g *Governor) Clean(token Token, cumulative Amount) error {
	x, err := g.get(token)
	if err != nil {
		return err
	}
	if cumulative.within(x.Cleaned) {
		return nil
	}
	if !x.Cleaned.within(cumulative) || !cumulative.within(x.Used) {
		return ErrOrder
	}
	delta := cumulative.sub(x.Cleaned)
	x.Cleaned = cumulative
	g.release(x, delta)
	g.retire(x)
	return nil
}
func (g *Governor) retire(x *Grant) {
	if x.Closed && x.Cleaned == x.Used {
		delete(g.grants, x.Token.ID)
	}
}
func (g *Governor) Held(account, user string) (Amount, error) {
	a := g.accounts[account]
	if a == nil {
		return Amount{}, ErrScope
	}
	if user == "" {
		return a.held, nil
	}
	if _, ok := a.config.UserShares[user]; !ok {
		return Amount{}, ErrScope
	}
	return a.byUser[user], nil
}
func (g *Governor) Outstanding() []Grant {
	result := make([]Grant, 0, len(g.grants))
	for _, x := range g.grants {
		result = append(result, *x)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Token.ID < result[j].Token.ID })
	return result
}
func (g *Governor) Check() error {
	held := map[string]Amount{}
	users := map[string]map[string]Amount{}
	for _, x := range g.grants {
		if !x.Used.within(x.Limit) || !x.Cleaned.within(x.Used) {
			return ErrOrder
		}
		amount := x.Limit
		if x.Closed {
			amount = x.Used
		}
		amount = amount.sub(x.Cleaned)
		held[x.Account] = held[x.Account].add(amount)
		if users[x.Account] == nil {
			users[x.Account] = map[string]Amount{}
		}
		users[x.Account][x.Token.User] = users[x.Account][x.Token.User].add(amount)
	}
	for id, a := range g.accounts {
		if held[id] != a.held || !a.held.within(a.config.DataLimit) {
			return ErrLimit
		}
		for user, n := range a.byUser {
			if n != users[id][user] || !n.within(a.config.UserShares[user]) {
				return ErrLimit
			}
		}
	}
	return nil
}
