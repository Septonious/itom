package mailbox

import (
	"context"
	"errors"
	"time"
)

// CheckFolders checks setup on a bounded fresh connection. Only the known
// provider mailbox-state rejection gets one retry; APPEND and stored-UID
// deletion/absence proofs are never retried by this helper.
func CheckFolders(ctx context.Context, cfg Config, folders []string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return checkFolders(ctx, folders, func(ctx context.Context) (*Client, error) { return Dial(ctx, cfg, folders) })
}

// CheckExistingFolders performs the same bounded SELECT/UIDVALIDITY checks as
// setup, without CREATE. It is suitable while the transport is online.
func CheckExistingFolders(ctx context.Context, cfg Config, folders []string) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return checkFoldersWith(ctx, folders, func(ctx context.Context) (*Client, error) { return Dial(ctx, cfg, folders) }, func(c *Client, ctx context.Context, folder string) error {
		return c.operation(ctx, folder, func() error { _, e := c.selectFolder(folder); return e })
	})
}
func checkFolders(ctx context.Context, folders []string, dial func(context.Context) (*Client, error)) error {
	return checkFoldersWith(ctx, folders, dial, func(c *Client, ctx context.Context, folder string) error { return c.Ensure(ctx, folder) })
}
func checkFoldersWith(ctx context.Context, folders []string, dial func(context.Context) (*Client, error), verify func(*Client, context.Context, string) error) error {
	for attempt := 0; attempt < 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		client, err := dial(ctx)
		if err == nil {
			for _, folder := range folders {
				if err = verify(client, ctx, folder); err != nil {
					break
				}
			}
			_ = client.Close()
		}
		if err == nil || attempt == 1 || !errors.Is(err, ErrMailboxStateChanged) {
			return err
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ErrMailboxStateChanged
}
