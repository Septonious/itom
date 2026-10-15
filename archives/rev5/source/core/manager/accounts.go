package manager

import (
	"context"
	"errors"
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"strings"
	"time"
)

type accountAdmin interface {
	Folders(context.Context) ([]mailbox.Folder, error)
	PurgeAll(context.Context) (mailbox.PurgeReport, error)
	Close() error
}

func (u *UI) accountMaintenance(cfg servercfg.Config, choice string) error {
	target, err := u.selectAccount(cfg)
	if err != nil {
		return err
	}
	if target.ID == "" {
		return nil
	}
	id := target.Mail.Username
	fmt.Fprintf(u.Out, "Account %s at %q\n", target.Mail.Username, target.Mail.Host)
	if choice == "3" {
		fmt.Fprintln(u.Out, "Removes the account connection from ITOM. Provider mailbox, messages and pending-write journals are preserved. Stop client VPNs and export updated profiles after removal.")
		if err = u.stopped(); err != nil {
			return err
		}
		err = servercfg.Update(u.db(), func(c *servercfg.Config) error {
			for _, a := range c.Accounts {
				if strings.EqualFold(a.Mail.Username, id) && a != target {
					return errors.New("account configuration changed; repeat selection")
				}
			}
			return c.RemoveAccount(target.ID)
		})
		if err == nil {
			fmt.Fprintln(u.Out, "Account removed from ITOM. Export updated profiles using menu item 5.")
		}
		return err
	}
	fmt.Fprintln(u.Out, "Permanently deletes ALL accessible folders and messages in this account, including non-ITOM mail. INBOX and folders protected by the provider are emptied. Stop all clients using this account. Other accounts are unaffected.")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	dial := u.adminDial
	if dial == nil {
		dial = func(ctx context.Context, cfg mailbox.Config) (accountAdmin, error) {
			return mailbox.DialAdmin(ctx, cfg)
		}
	}
	admin, err := dial(ctx, target.Mail)
	if err != nil {
		return err
	}
	defer admin.Close()
	folders, err := admin.Folders(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(u.Out, "Folders found: %d\n", len(folders))
	for _, f := range folders {
		fmt.Fprintf(u.Out, "  %q\n", f.Name)
	}
	if err = u.stopped(); err != nil {
		return err
	}
	current, err := servercfg.Load(u.db())
	if err != nil {
		return err
	}
	unchanged := false
	for _, a := range current.Accounts {
		if a == target {
			unchanged = true
		}
	}
	if !unchanged {
		return errors.New("account configuration changed; repeat selection")
	}
	fmt.Fprintln(u.Out, "Purging…")
	report, err := admin.PurgeAll(ctx)
	labels := map[string]string{"DELETED": "folder deleted", "KEPT_EMPTY": "folder retained and emptied", "NOT_EMPTIED": "purge not confirmed", "UNKNOWN": "outcome not confirmed", "UNSUBSCRIBE_REFUSED": "could not remove subscription"}
	for _, item := range report.Items {
		fmt.Fprintf(u.Out, "%q — %s\n", item.Name, labels[item.Outcome])
	}
	if report.Verified {
		fmt.Fprintln(u.Out, "Verification complete. Remaining folders:")
		for _, name := range report.Remaining {
			fmt.Fprintf(u.Out, "  %q\n", name)
		}
	} else {
		fmt.Fprintln(u.Out, "Full purge was not confirmed. The result may be partial; open this option again to inspect folders.")
	}
	fmt.Fprintln(u.Out, "ITOM configuration and journals are preserved. Required folders are recreated at startup. Purging does not refund uncertain quota debt.")
	return err
}

func (u *UI) waitAdmin(ctx context.Context, d time.Duration) error {
	if u.pauseAdmin != nil {
		return u.pauseAdmin(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// purgeAllAccounts is deliberately serial. It never opens two destructive
// admin connections at once and inserts a cool-down between accounts so a
// provider does not see a burst of DELETE/EXPUNGE operations from ITOM.
func (u *UI) purgeAllAccounts(cfg servercfg.Config) error {
	if len(cfg.Accounts) == 0 {
		return errors.New("no mail accounts")
	}
	fmt.Fprintln(u.Out, "FULL PURGE: removes all accessible messages and user folders in ALL configured mail accounts. Protected system folders are emptied. Accounts are processed serially with pauses.")
	var failures []string
	for i, account := range cfg.Accounts {
		if i > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := u.waitAdmin(ctx, 2*time.Second)
			cancel()
			if err != nil {
				return err
			}
		}
		fmt.Fprintf(u.Out, "\n[%d/%d] %s — purge started\n", i+1, len(cfg.Accounts), account.Mail.Username)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		dial := u.adminDial
		if dial == nil {
			dial = func(ctx context.Context, cfg mailbox.Config) (accountAdmin, error) {
				return mailbox.DialAdmin(ctx, cfg)
			}
		}
		admin, err := dial(ctx, account.Mail)
		if err != nil {
			cancel()
			failures = append(failures, account.Mail.Username+": "+err.Error())
			fmt.Fprintf(u.Out, "%s — connection failed: %v\n", account.Mail.Username, err)
			continue
		}
		report, purgeErr := admin.PurgeAll(ctx)
		closeErr := admin.Close()
		cancel()
		for _, item := range report.Items {
			fmt.Fprintf(u.Out, "%s | %q — %s\n", account.Mail.Username, item.Name, item.Outcome)
		}
		if purgeErr == nil && closeErr == nil && report.Verified {
			fmt.Fprintf(u.Out, "%s — purge confirmed\n", account.Mail.Username)
			continue
		}
		if purgeErr == nil {
			purgeErr = closeErr
		}
		if purgeErr == nil {
			purgeErr = errors.New("IMAP_PURGE_INCOMPLETE")
		}
		failures = append(failures, account.Mail.Username+": "+purgeErr.Error())
		fmt.Fprintf(u.Out, "%s — purge not confirmed: %v\n", account.Mail.Username, purgeErr)
	}
	if len(failures) != 0 {
		return fmt.Errorf("full purge completed with errors: %s", strings.Join(failures, "; "))
	}
	fmt.Fprintln(u.Out, "\nAll mail accounts were purged serially. ITOM configuration and physical-debt journals were preserved.")
	return nil
}
