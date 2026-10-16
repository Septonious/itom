package mailbox

import (
	"context"
	imap "github.com/emersion/go-imap/v2"
)

// AppendHint is a bounded discovery aid from a successful APPENDUID response.
// It is not a body observation or a deletion/refund proof. Unknown APPENDs and
// providers without UIDPLUS use ordinary mailbox discovery instead.
type AppendHint struct {
	MIMEBytes     uint64
	Folder        string
	Validity, UID uint32
	Digest        [32]byte
}

func (c *Client) AppendHints(folder string, wanted map[[32]byte]bool) []AppendHint {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []AppendHint
	for _, hint := range c.appendHints {
		if hint.Folder == folder && wanted[hint.Digest] {
			out = append(out, hint)
		}
	}
	return out
}

// FetchExact reads at most 32 immutable UID bodies under an explicitly checked
// UIDVALIDITY. The caller still has to match each digest and prove deletion.
func (c *Client) FetchExact(ctx context.Context, folder string, validity uint32, ids []uint32) ([]Message, error) {
	if validity == 0 || len(ids) == 0 || len(ids) > 32 {
		return nil, ErrLimit
	}
	var out []Message
	err := c.operation(ctx, folder, func() error {
		selected, err := c.selectFolder(folder)
		if err != nil {
			return err
		}
		if selected.UIDValidity != validity {
			return ErrChanged
		}
		uids := make([]imap.UID, len(ids))
		seen := map[uint32]bool{}
		for i, uid := range ids {
			if uid == 0 || seen[uid] {
				return ErrScope
			}
			seen[uid] = true
			uids[i] = imap.UID(uid)
		}
		out, err = c.fetchSet(imap.UIDSetNum(uids...), len(uids), true)
		for i := range out {
			out[i].Folder = folder
			out[i].Validity = validity
		}
		return err
	})
	return out, err
}
