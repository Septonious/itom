package mailbox

import (
	"context"
	"errors"
	"fmt"
	imap "github.com/emersion/go-imap/v2"
	"sort"
	"strings"
	"time"
)

// Admin is deliberately separate from the transport's folder-scoped Client.
// Only a confirmed, offline maintenance operation should use it.
type Admin struct {
	client *Client
	pace   time.Duration
}
type Folder struct {
	Name      string
	Delimiter rune
}
type PurgeItem struct{ Name, Outcome string }
type PurgeReport struct {
	Items     []PurgeItem
	Remaining []string
	Verified  bool
}

func DialAdmin(ctx context.Context, cfg Config) (*Admin, error) {
	c, err := dialMode(ctx, cfg, nil, nil, true)
	if err != nil {
		return nil, err
	}
	return &Admin{client: c, pace: 350 * time.Millisecond}, nil
}
func (a *Admin) Close() error { return a.client.Close() }
func (a *Admin) wait(ctx context.Context) error {
	if a.pace <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(a.pace)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func commandRefused(err error) bool {
	var status *imap.Error
	return errors.As(err, &status) && status.Type == imap.StatusResponseTypeNo
}

// A tagged NO is a known command refusal, not an ambiguous broken connection.
func (a *Admin) command(ctx context.Context, fn func() error) error {
	var refused error
	err := a.client.operation(ctx, "", func() error {
		err := fn()
		if commandRefused(err) {
			refused = err
			return nil
		}
		return err
	})
	if err != nil {
		return err
	}
	return refused
}

func (a *Admin) Folders(ctx context.Context) ([]Folder, error) {
	var result []Folder
	err := a.command(ctx, func() error {
		cmd := a.client.c.List("", "*", nil)
		defer cmd.Close()
		seen := map[string]bool{}
		for row := cmd.Next(); row != nil; row = cmd.Next() {
			if len(result) >= 4096 || len(row.Mailbox) == 0 || len(row.Mailbox) > 1024 || strings.ContainsAny(row.Mailbox, "\r\n\x00") || seen[row.Mailbox] {
				a.client.conn.Close()
				return ErrLimit
			}
			seen[row.Mailbox] = true
			result = append(result, Folder{Name: row.Mailbox, Delimiter: row.Delim})
		}
		return cmd.Close()
	})
	return result, err
}

func folderDepth(f Folder) int {
	if f.Delimiter == 0 {
		return 0
	}
	return strings.Count(f.Name, string(f.Delimiter))
}

// Empty uses silent UID STORE then CLOSE (no unbounded EXPUNGE response list).
// It checks that the mailbox is empty after completion. New incoming mail can
// make that check fail; a bounded maintenance pass must not chase it forever.
func (a *Admin) empty(ctx context.Context, name string) error {
	var count uint32
	if err := a.command(ctx, func() error {
		s, err := a.client.selectFolder(name)
		if err == nil {
			count = s.NumMessages
		}
		return err
	}); err != nil {
		return err
	}
	if count != 0 {
		if err := a.command(ctx, func() error {
			return a.client.c.Store(imap.UIDSet{{Start: 1, Stop: 0}}, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close()
		}); err != nil {
			return err
		}
	}
	if err := a.command(ctx, func() error { return a.client.c.UnselectAndExpunge().Wait() }); err != nil {
		return err
	}
	if err := a.wait(ctx); err != nil {
		return err
	}
	if err := a.command(ctx, func() error {
		s, err := a.client.selectFolder(name)
		if err == nil {
			count = s.NumMessages
		}
		return err
	}); err != nil {
		return err
	}
	if err := a.command(ctx, func() error { return a.client.c.UnselectAndExpunge().Wait() }); err != nil {
		return err
	}
	if err := a.wait(ctx); err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("IMAP_MAILBOX_NOT_EMPTY")
	}
	return nil
}

// PurgeAll is explicitly destructive to ALL folders and mail in this account,
// not only ITOM messages. It never resets transport journals or quota debt.
// Errors return the partial report; a network interruption is never success.
func (a *Admin) PurgeAll(ctx context.Context) (PurgeReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var report PurgeReport
	folders, err := a.Folders(ctx)
	if err != nil {
		return report, err
	}
	sort.Slice(folders, func(i, j int) bool {
		x, y := folderDepth(folders[i]), folderDepth(folders[j])
		if x != y {
			return x > y
		}
		return folders[i].Name > folders[j].Name
	})
	keptEmpty := map[string]bool{}
	failed := false
	for i, f := range folders {
		if i > 0 {
			if err = a.wait(ctx); err != nil {
				return report, err
			}
		}
		deleted := false
		if !strings.EqualFold(f.Name, "INBOX") {
			err = a.command(ctx, func() error { return a.client.c.Delete(f.Name).Wait() })
			if err == nil {
				deleted = true
			} else if !commandRefused(err) {
				report.Items = append(report.Items, PurgeItem{f.Name, "UNKNOWN"})
				return report, err
			}
		}
		if deleted {
			report.Items = append(report.Items, PurgeItem{f.Name, "DELETED"})
			err = a.command(ctx, func() error { return a.client.c.Unsubscribe(f.Name).Wait() })
			if err != nil && !commandRefused(err) {
				return report, err
			}
			if err != nil {
				report.Items = append(report.Items, PurgeItem{f.Name, "UNSUBSCRIBE_REFUSED"})
			}
			continue
		}
		err = a.empty(ctx, f.Name)
		if err != nil {
			report.Items = append(report.Items, PurgeItem{f.Name, "NOT_EMPTIED"})
			failed = true
			if !commandRefused(err) {
				return report, err
			}
		} else {
			// Some providers refuse DELETE until the mailbox is empty. Retry only
			// after the first DELETE returned a definite NO and emptying succeeded.
			if !strings.EqualFold(f.Name, "INBOX") {
				err = a.command(ctx, func() error { return a.client.c.Delete(f.Name).Wait() })
				if err == nil {
					report.Items = append(report.Items, PurgeItem{f.Name, "DELETED"})
					err = a.command(ctx, func() error { return a.client.c.Unsubscribe(f.Name).Wait() })
					if err != nil && !commandRefused(err) {
						return report, err
					}
					if err != nil {
						report.Items = append(report.Items, PurgeItem{f.Name, "UNSUBSCRIBE_REFUSED"})
					}
					continue
				}
				if !commandRefused(err) {
					report.Items = append(report.Items, PurgeItem{f.Name, "UNKNOWN"})
					return report, err
				}
			}
			keptEmpty[f.Name] = true
			report.Items = append(report.Items, PurgeItem{f.Name, "KEPT_EMPTY"})
		}
	}
	after, err := a.Folders(ctx)
	if err != nil {
		return report, err
	}
	for _, f := range after {
		report.Remaining = append(report.Remaining, f.Name)
		if !keptEmpty[f.Name] {
			failed = true
		}
	}
	report.Verified = !failed
	if failed {
		return report, errors.New("IMAP_PURGE_INCOMPLETE")
	}
	return report, nil
}
