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
	mu                                sync.Mutex
	limit, accountLimit, active, peak int
	accounts                          map[string]int
	logins                            chan struct{}
}

func newSocketAdmission(total, perAccount, logins int) *socketAdmission {
	return &socketAdmission{limit: total, accountLimit: perAccount, accounts: map[string]int{}, logins: make(chan struct{}, logins)}
}

func (b *socketAdmission) acquire(account string) (func(), error) {
	b.mu.Lock()
	if b.active >= b.limit || b.accounts[account] >= b.accountLimit {
		b.mu.Unlock()
		return nil, errLocalConnectionLimit
	}
	b.active++
	b.accounts[account]++
	b.peak = max(b.peak, b.active)
	b.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.active--
			b.accounts[account]--
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
		release, err := b.acquire(account)
		if err != nil {
			return nil, err
		}
		select {
		case b.logins <- struct{}{}:
		case <-ctx.Done():
			release()
			return nil, ctx.Err()
		}
		client, err := dial(ctx, cfg, folders)
		<-b.logins
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
