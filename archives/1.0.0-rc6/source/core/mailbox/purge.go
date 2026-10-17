package mailbox

import (
	"context"
	"time"
)

// PurgeFolder removes every currently existing message from one folder that is
// already inside this transport client's explicit scope. It is intentionally
// paced and bounded. New mail arriving during verification makes the pass fail
// closed so a caller never reports a concurrent mailbox as clean.
func (c *Client) PurgeFolder(ctx context.Context, folder string, pace time.Duration) (int, error) {
	cur := Cursor{}
	removed := 0
	for pages := 0; pages < 1024; pages++ {
		rows, next, err := c.Page(ctx, folder, cur)
		if err != nil {
			return removed, err
		}
		if len(rows) > 0 {
			if err = c.DeleteMany(ctx, rows); err != nil {
				return removed, err
			}
			removed += len(rows)
			if removed > 16384 {
				return removed, ErrLimit
			}
			if pace > 0 {
				t := time.NewTimer(pace)
				select {
				case <-ctx.Done():
					t.Stop()
					return removed, ctx.Err()
				case <-t.C:
				}
			}
		}
		if next == cur {
			break
		}
		cur = next
	}
	// Re-scan from the mailbox origin. UID holes from our expunges are skipped;
	// any returned row is a concurrent/new message and must not be silently lost.
	rows, _, err := c.Page(ctx, folder, Cursor{})
	if err != nil {
		return removed, err
	}
	if len(rows) != 0 {
		return removed, ErrBusy
	}
	return removed, nil
}
