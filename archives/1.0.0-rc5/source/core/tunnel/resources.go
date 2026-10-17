package tunnel

import (
	"context"
	"errors"
	"strings"
	"sync"

	"itom.local/core/mailbox"
)

var errLocalConnectionLimit = errors.New("IMAP_LOCAL_CONNECTION_LIMIT")

// Socket and login limits are separate from journal/wallet and logical credit.
// Both reservations precede dial; a failed dial or actual socket closure returns
// service capacity once, including asynchronous IMAP reader failures.
type socketAdmission struct {
	mu                                             sync.Mutex
	limit, accountLimit, active, peak              int
	accounts                                       map[string]int
	controls                                       map[string]int
	controlActive, controlPerAccount, controlTotal int
	logins                                         *loginAdmission
}

func newSocketAdmission(total, perAccount, logins int) *socketAdmission {
	return &socketAdmission{limit: total, accountLimit: perAccount, accounts: map[string]int{}, controls: map[string]int{}, logins: &loginAdmission{limit: logins}}
}

type controlAdmissionKey struct{}

func controlAdmission(ctx context.Context) context.Context {
	return context.WithValue(ctx, controlAdmissionKey{}, true)
}

// Configure before sharing the budget. Optional capacity can be lent only
// while every required base connection has its own actual reservation.
func (b *socketAdmission) reserveControls(perAccount, total int) {
	b.controlPerAccount, b.controlTotal = min(perAccount, b.accountLimit), min(total, b.limit)
}

func (b *socketAdmission) acquire(account string) (func(), error) {
	return b.acquireRole(account, false)
}
func (b *socketAdmission) acquireRole(account string, control bool) (func(), error) {
	b.mu.Lock()
	globalLimit, accountLimit := b.limit, b.accountLimit
	if !control {
		globalLimit -= max(0, b.controlTotal-b.controlActive)
		accountLimit -= max(0, b.controlPerAccount-b.controls[account])
	}
	if b.active >= globalLimit || b.accounts[account] >= accountLimit {
		b.mu.Unlock()
		return nil, errLocalConnectionLimit
	}
	b.active++
	b.accounts[account]++
	if control {
		b.controls[account]++
		b.controlActive++
	}
	b.peak = max(b.peak, b.active)
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			b.accounts[account]--
			if control {
				b.controlActive--
				b.controls[account]--
				if b.controls[account] == 0 {
					delete(b.controls, account)
				}
			}
			if b.accounts[account] == 0 {
				delete(b.accounts, account)
			}
			b.mu.Unlock()
		})
	}, nil
}

func (b *socketAdmission) wrap(dial Dial) Dial {
	return func(ctx context.Context, cfg mailbox.Config, folders []string) (*mailbox.Client, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		account := strings.ToLower(cfg.Host) + "\x00" + strings.ToLower(cfg.Username)
		control, _ := ctx.Value(controlAdmissionKey{}).(bool)
		release, err := b.acquireRole(account, control)
		if err != nil {
			return nil, err
		}
		releaseLogin, err := b.logins.acquire(ctx, priorityLogin(ctx))
		if err != nil {
			release()
			return nil, err
		}
		client, err := dial(ctx, cfg, folders)
		releaseLogin()
		if err != nil {
			release()
			return nil, err
		}
		client.OnClose(release)
		return client, nil
	}
}

type accountAdmission struct {
	slots       chan struct{}
	orderMu     sync.Mutex
	orderCond   *sync.Cond
	nextInitial int
}

func newAccountAdmission(socketLimit, accountFootprint int) *accountAdmission {
	b := &accountAdmission{slots: make(chan struct{}, max(1, socketLimit/accountFootprint))}
	b.orderCond = sync.NewCond(&b.orderMu)
	return b
}
func (b *accountAdmission) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case b.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (b *accountAdmission) release() { <-b.slots }

// Config order is shared by both endpoints. Initial activation must not select
// unrelated random subsets when a large profile exceeds the active budget.
func (b *accountAdmission) acquireInitial(ctx context.Context, index int) error {
	stop := context.AfterFunc(ctx, func() { b.orderMu.Lock(); b.orderCond.Broadcast(); b.orderMu.Unlock() })
	defer stop()
	b.orderMu.Lock()
	defer b.orderMu.Unlock()
	for index != b.nextInitial && ctx.Err() == nil {
		b.orderCond.Wait()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := b.acquire(ctx); err != nil {
		return err
	}
	b.nextInitial++
	b.orderCond.Broadcast()
	return nil
}
