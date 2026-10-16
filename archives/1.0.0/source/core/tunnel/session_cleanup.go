package tunnel

import (
	"context"
	"fmt"
	"sort"
	"time"

	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
)

const (
	sessionCleanupAccountPause = time.Second
	sessionMailFallbackTTL     = 15 * time.Minute
)

// cleanupUserMail is the automatic idle janitor. It may remove only unjournaled
// pre-auth handshake garbage, whose format is independently recognizable. DATA
// and authenticated control mail are deliberately retained: deleting them
// outside the journal would destroy remote absence proof and could leave durable
// quota/debt charged forever.
func cleanupUserMail(ctx context.Context, cfg servercfg.Config, u servercfg.User, log func(string)) error {
	return cleanupUserMailWithDial(ctx, cfg, u, log, mailbox.Dial)
}
func cleanupUserMailWithDial(ctx context.Context, cfg servercfg.Config, u servercfg.User, log func(string), dial Dial) error {
	accounts := append([]servercfg.Account(nil), cfg.Accounts...)
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	for i, a := range accounts {
		if i > 0 {
			if err := pause(ctx, sessionCleanupAccountPause); err != nil {
				return err
			}
		}
		accountCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		mail, err := dial(accountCtx, a.Mail, []string{u.Up, u.Down})
		if err != nil {
			cancel()
			return fmt.Errorf("session cleanup %s dial: %w", a.ID, err)
		}
		for _, folder := range []string{u.Up, u.Down} {
			removed, cleanupErr := cleanupAutomaticFolder(accountCtx, mail, folder)
			if cleanupErr != nil {
				mail.Close()
				cancel()
				return fmt.Errorf("session cleanup %s %s: %w", a.ID, folder, cleanupErr)
			}
			if log != nil && removed > 0 {
				log(fmt.Sprintf("account=%s user=%s event=session_preauth_cleanup folder=%s removed=%d", a.ID, u.ID, folder, removed))
			}
		}
		closeErr := mail.Close()
		cancel()
		if closeErr != nil {
			return fmt.Errorf("session cleanup %s close: %w", a.ID, closeErr)
		}
	}
	return nil
}

func cleanupAutomaticFolder(ctx context.Context, mail mailbox.Transport, folder string) (int, error) {
	if err := mail.Ensure(ctx, folder); err != nil {
		return 0, fmt.Errorf("ensure: %w", err)
	}
	rows, err := inventory(ctx, mail, folder)
	if err != nil {
		return 0, fmt.Errorf("inventory: %w", err)
	}
	stale := stalePreauthRows(rows)
	if err = deleteMailboxRows(ctx, mail, stale); err != nil {
		return 0, fmt.Errorf("preauth: %w", err)
	}
	return len(stale), nil
}

func runFallbackMailJanitor(ctx context.Context, cfg servercfg.Config, u servercfg.User, hub *stripeHub, gate chan struct{}, log func(string)) {
	runFallbackMailJanitorWithDial(ctx, cfg, u, hub, gate, log, mailbox.Dial)
}
func runFallbackMailJanitorWithDial(ctx context.Context, cfg servercfg.Config, u servercfg.User, hub *stripeHub, gate chan struct{}, log func(string), dial Dial) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			epoch, ok := hub.beginCleanup(sessionMailFallbackTTL)
			if !ok {
				continue
			}
			select {
			case <-ctx.Done():
				hub.endCleanup(epoch, false)
				return
			case <-gate:
			}
			err := cleanupUserMailWithDial(ctx, cfg, u, log, dial)
			gate <- struct{}{}
			hub.endCleanup(epoch, err == nil)
			if log != nil {
				if err != nil {
					log(fmt.Sprintf("user=%s event=session_preauth_ttl_cleanup code=%v", u.ID, err))
				} else {
					log(fmt.Sprintf("user=%s event=session_preauth_ttl_cleanup code=OK", u.ID))
				}
			}
		}
	}
}
