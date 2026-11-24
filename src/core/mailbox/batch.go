package mailbox

import (
	"context"
	"crypto/sha256"
	imap "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// DeleteMany preserves Delete's proof requirements but amortizes SELECT, full
// body verification, STORE, EXPUNGE and absence checks across at most 32 UIDs.
// On any partial failure the caller keeps every journal debt for recovery.
func (c *Client) DeleteMany(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	if len(messages) > 32 {
		return ErrLimit
	}
	folder, validity := messages[0].Folder, messages[0].Validity
	expected := map[uint32][32]byte{}
	var ids []imap.UID
	for _, m := range messages {
		if m.Folder != folder || validity == 0 || m.Validity != validity || m.UID == 0 {
			return ErrScope
		}
		if _, ok := expected[m.UID]; ok {
			return ErrScope
		}
		expected[m.UID] = sha256.Sum256(m.MIME)
		ids = append(ids, imap.UID(m.UID))
	}
	set := imap.UIDSetNum(ids...)
	return c.operation(ctx, folder, func() error {
		s, e := c.selectFolder(folder)
		if e != nil {
			return e
		}
		if s.UIDValidity != validity {
			return ErrChanged
		}
		rows, e := c.fetchSet(set, len(ids), true)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if sha256.Sum256(row.MIME) != expected[row.UID] {
				return ErrScope
			}
		}
		plus := c.c.Caps().Has(imap.CapUIDPlus) || c.c.Caps().Has(imap.CapIMAP4rev2)
		if !plus && len(rows) > 0 {
			if s.NumMessages > 4096 {
				return ErrLimit
			}
			c.conn.left.Store(1 << 20)
			cmd := c.c.Fetch(imap.UIDSet{{Start: 1, Stop: 0}}, &imap.FetchOptions{UID: true, Flags: true})
			safe := true
			for row := cmd.Next(); row != nil; row = cmd.Next() {
				var uid imap.UID
				deleted := false
				for item := row.Next(); item != nil; item = row.Next() {
					switch x := item.(type) {
					case imapclient.FetchItemDataUID:
						uid = x.UID
					case imapclient.FetchItemDataFlags:
						for _, flag := range x.Flags {
							if flag == imap.FlagDeleted {
								deleted = true
							}
						}
					}
				}
				if deleted && !set.Contains(uid) {
					safe = false
				}
			}
			if e = cmd.Close(); e != nil {
				return e
			}
			if !safe {
				return ErrScope
			}
		}
		if len(rows) > 0 {
			if e = c.c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); e != nil {
				return e
			}
			if plus {
				// UIDPLUS gives exact-set deletion semantics. UIDVALIDITY and full
				// body digests were verified above, so a successful UID EXPUNGE is
				// the deletion proof for precisely these physical messages.
				if e = c.c.UIDExpunge(set).Close(); e != nil {
					return e
				}
				return nil
			}
			if e = c.c.Expunge().Close(); e != nil {
				return e
			}
		}
		// Without UIDPLUS, retain the conservative post-EXPUNGE absence proof.
		s, e = c.selectFolder(folder)
		if e != nil {
			return e
		}
		if s.UIDValidity != validity {
			return ErrChanged
		}
		rows, e = c.fetchSet(set, len(ids), false)
		if e != nil {
			return e
		}
		if len(rows) > 0 {
			return ErrScope
		}
		return nil
	})
}

// DeleteObservedMany removes a bounded exact UID set that the caller has
// already observed and content-verified against its durable journal. IMAP
// messages are immutable, and UIDPLUS provides exact-set expunge semantics, so
// repeating a full-body FETCH after that observation would not add proof. A
// changed UIDVALIDITY still fences the deletion. Providers without UIDPLUS use
// DeleteMany and retain its conservative full-body proof and absence checks.
//
// This is intentionally separate from DeleteMany: arbitrary callers must not
// treat a stale Message value as proof that the current physical UID has the
// expected body.
func (c *Client) DeleteObservedMany(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	if len(messages) > 32 {
		return ErrLimit
	}
	if !(c.c.Caps().Has(imap.CapUIDPlus) || c.c.Caps().Has(imap.CapIMAP4rev2)) {
		return c.DeleteMany(ctx, messages)
	}
	folder, validity := messages[0].Folder, messages[0].Validity
	seen := make(map[uint32]struct{}, len(messages))
	ids := make([]imap.UID, 0, len(messages))
	for _, m := range messages {
		if m.Folder != folder || validity == 0 || m.Validity != validity || m.UID == 0 {
			return ErrScope
		}
		if _, ok := seen[m.UID]; ok {
			return ErrScope
		}
		seen[m.UID] = struct{}{}
		ids = append(ids, imap.UID(m.UID))
	}
	set := imap.UIDSetNum(ids...)
	return c.operation(ctx, folder, func() error {
		selected, err := c.selectFolder(folder)
		if err != nil {
			return err
		}
		if selected.UIDValidity != validity {
			return ErrChanged
		}
		// The successful SELECT above fences this immutable, already observed
		// exact UID set. Pipeline only these two ordered UID commands, never
		// the validity check. Join BOTH outcomes before exposing success:
		// any failure retains all journal debt, even if deletion took effect.
		store := c.c.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil)
		expunge := c.c.UIDExpunge(set)
		storeErr := store.Close()
		expungeErr := expunge.Close()
		if storeErr != nil {
			return storeErr
		}
		return expungeErr
	})
}
