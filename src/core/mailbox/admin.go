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
	cfg    Config
}
type Folder struct {
	Name      string
	Delimiter rune
	Attrs     []imap.MailboxAttr
}
type PurgeItem struct{ Name, Outcome, Reason string }
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
	return &Admin{client: c, pace: 350 * time.Millisecond, cfg: cfg}, nil
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
			result = append(result, Folder{Name: row.Mailbox, Delimiter: row.Delim, Attrs: append([]imap.MailboxAttr(nil), row.Attrs...)})
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

// protectedLegacyFolder recognizes provider-broken retired folders. Never SELECT,
// STORE, DELETE or UNSUBSCRIBE these names (including descendants). A protected
// folder's ancestors are also retained to avoid recursive provider deletion.
func protectedLegacyFolder(f Folder) bool {
	parts := []string{f.Name}
	if f.Delimiter != 0 {
		parts = strings.Split(f.Name, string(f.Delimiter))
	}
	for _, part := range parts {
		name := strings.ToLower(part)
		if name == "session-v2" || strings.HasSuffix(name, "-session-v2") {
			return true
		}
		tokens := strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' || r == ' ' || r == '.' })
		if len(tokens) > 1 && tokens[0] == "wsit" {
			for _, token := range tokens[1:] {
				if token == "s2c" {
					return true
				}
			}
		}
	}
	return false
}
func protectedPurgeFolders(folders []Folder) map[string]bool {
	protected := map[string]bool{}
	for _, f := range folders {
		if !protectedLegacyFolder(f) {
			continue
		}
		protected[f.Name] = true
		if f.Delimiter != 0 {
			sep := string(f.Delimiter)
			for _, parent := range folders {
				if strings.HasPrefix(f.Name, parent.Name+sep) {
					protected[parent.Name] = true
				}
			}
		}
	}
	return protected
}

// Retain built-in mailboxes even when DELETE would return OK. Rambler
// recreates these folders. DELETE is not evidence of an empty mailbox.
// Match root names case-insensitively and honor roles advertised by LIST.
func retainSystemFolder(f Folder) bool {
	switch strings.ToLower(f.Name) {
	case "inbox", "trash", "spam", "sentbox", "draftbox", "sent", "sent items", "sent mail", "drafts", "junk", "junk email", "deleted items":
		return true
	}
	for _, attr := range f.Attrs {
		switch strings.ToLower(string(attr)) {
		case `\all`, `\archive`, `\drafts`, `\flagged`, `\junk`, `\sent`, `\trash`, `\important`:
			return true
		}
	}
	return false
}

// A tiny command boundary lets regression tests inject provider EOFs without
// changing the real admin connection, scoped commands, or retention journals.
type purgeBackend interface {
	Folders(context.Context) ([]Folder, error)
	deleteFolder(context.Context, string) error
	unsubscribeFolder(context.Context, string) error
	empty(context.Context, string) error
	wait(context.Context) error
	reopen(context.Context) error
}

func (a *Admin) deleteFolder(ctx context.Context, name string) error {
	return a.command(ctx, func() error { return a.client.c.Delete(name).Wait() })
}
func (a *Admin) unsubscribeFolder(ctx context.Context, name string) error {
	return a.command(ctx, func() error { return a.client.c.Unsubscribe(name).Wait() })
}
func (a *Admin) reopen(ctx context.Context) error {
	_ = a.Close() // the previous mutation may have completed; never reuse its socket
	if err := a.wait(ctx); err != nil {
		return err
	}
	fresh, err := DialAdmin(ctx, a.cfg)
	if err != nil {
		return err
	}
	a.client = fresh.client
	return nil
}

// PurgeAll is destructive to accessible mail except explicitly protected legacy
// folders. It never resets transport journals or quota debt. Verification is
// scoped to non-protected folders; protected names are reported, not emptied.
func (a *Admin) PurgeAll(ctx context.Context) (PurgeReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	return purgeFolders(ctx, a)
}
func purgeFolders(ctx context.Context, a purgeBackend) (PurgeReport, error) {
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
	protected := map[string]bool{}
	retained := map[string]bool{}
	keptEmpty := map[string]bool{}
	unresolved := map[string]error{}
	present := map[string]bool{}
	// Keep protections even if a subsequent LIST omits attributes.
	remember := func(list []Folder) {
		present = map[string]bool{}
		for _, f := range list {
			present[f.Name] = true
			if retainSystemFolder(f) {
				retained[f.Name] = true
			}
		}
		for name := range protectedPurgeFolders(list) {
			protected[name] = true
		}
		// Avoid recursively deleting the ancestor of a retained system folder.
		for _, f := range list {
			if !retained[f.Name] || f.Delimiter == 0 {
				continue
			}
			for _, parent := range list {
				if strings.HasPrefix(f.Name, parent.Name+string(f.Delimiter)) {
					protected[parent.Name] = true
				}
			}
		}
	}
	remember(folders)
	recoveries := 0
	reportedProtected := map[string]bool{}
	process := func(f Folder, keep bool) error {
		if !present[f.Name] {
			return nil
		}
		if protected[f.Name] {
			if !reportedProtected[f.Name] {
				report.Items = append(report.Items, PurgeItem{Name: f.Name, Outcome: "SKIPPED_PROTECTED"})
				reportedProtected[f.Name] = true
			}
			return nil
		}
		if err := a.wait(ctx); err != nil {
			return err
		}
		outcome, err := purgeOne(ctx, a, f.Name, keep)
		report.Items = append(report.Items, PurgeItem{Name: f.Name, Outcome: outcome, Reason: FailureReason(err)})
		if err == nil {
			delete(unresolved, f.Name)
			if outcome == "KEPT_EMPTY" {
				keptEmpty[f.Name] = true
			}
			return nil
		}
		unresolved[f.Name] = err
		delete(keptEmpty, f.Name)
		if commandRefused(err) {
			return nil
		}
		// Preserve bounded fresh-socket recovery after ambiguous mutations.
		recoveries++
		if recoveries > 8 {
			return errors.Join(err, errors.New("IMAP_PURGE_RECOVERY_LIMIT"))
		}
		if err := a.reopen(ctx); err != nil {
			return err
		}
		fresh, err := a.Folders(ctx)
		if err != nil {
			return err
		}
		remember(fresh)
		return nil
	}
	// Delete ordinary folders first: the provider may move their contents
	// to Trash. Refresh inventory, then empty system folders in place.
	for _, f := range folders {
		if retained[f.Name] && !protected[f.Name] {
			continue
		}
		if err := process(f, false); err != nil {
			return report, err
		}
	}
	fresh, err := a.Folders(ctx)
	if err != nil {
		return report, err
	}
	remember(fresh)
	for _, f := range fresh {
		if !retained[f.Name] && !protected[f.Name] {
			continue
		}
		if err := process(f, true); err != nil {
			return report, err
		}
	}
	after, err := a.Folders(ctx)
	if err != nil {
		return report, err
	}
	remember(after)
	var unverified []string
	for _, f := range after {
		report.Remaining = append(report.Remaining, f.Name)
		if protected[f.Name] {
			if !reportedProtected[f.Name] {
				report.Items = append(report.Items, PurgeItem{Name: f.Name, Outcome: "SKIPPED_PROTECTED"})
			}
			continue
		}
		if !keptEmpty[f.Name] || unresolved[f.Name] != nil {
			unverified = append(unverified, f.Name)
		}
	}
	report.Verified = len(unverified) == 0
	if !report.Verified {
		sort.Strings(unverified)
		return report, fmt.Errorf("IMAP_PURGE_INCOMPLETE: remaining unverified folders %q", unverified)
	}
	return report, nil
}
func purgeOne(ctx context.Context, a purgeBackend, name string, keep bool) (string, error) {
	// Never DELETE or UNSUBSCRIBE a known system folder, regardless of
	// provider responses. Empty verifies zero messages after CLOSE.
	if keep {
		if err := a.empty(ctx, name); err != nil {
			return "NOT_EMPTIED", err
		}
		return "KEPT_EMPTY", nil
	}
	// Ordinary folders retain the tagged-NO fallback.
	err := a.deleteFolder(ctx, name)
	if err == nil {
		return finishDelete(ctx, a, name)
	}
	if !commandRefused(err) {
		return "UNKNOWN", err
	}
	if err := a.empty(ctx, name); err != nil {
		return "NOT_EMPTIED", err
	}
	err = a.deleteFolder(ctx, name)
	if err == nil {
		return finishDelete(ctx, a, name)
	}
	if !commandRefused(err) {
		return "UNKNOWN", err
	}
	return "KEPT_EMPTY", nil
}
func finishDelete(ctx context.Context, a purgeBackend, name string) (string, error) {
	err := a.unsubscribeFolder(ctx, name)
	if commandRefused(err) {
		return "DELETED", nil
	}
	if err != nil {
		return "DELETED", err
	}
	return "DELETED", nil
}
