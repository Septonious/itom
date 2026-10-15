package mailbox

import (
	"sort"

	imap "github.com/emersion/go-imap/v2"
)

// searchTailUIDs bounds both the wire response and expanded UID count. A UID
// SEARCH identifies candidates, never a complete BODY or absence evidence.
func (c *Client) searchTailUIDs(first, last uint32, limit int) ([]imap.UID, uint32, error) {
	if first == 0 || last < first || limit < 1 || limit > 16 {
		return nil, 0, ErrScope
	}
	c.conn.left.Store(128 << 10)
	found, err := c.c.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{{{Start: imap.UID(first), Stop: imap.UID(last)}}}}, nil).Wait()
	if err != nil {
		return nil, 0, err
	}
	set, ok := found.All.(imap.UIDSet)
	if !ok {
		return nil, 0, ErrScope
	}
	var ids []imap.UID
	for _, part := range set {
		if part.Start < imap.UID(first) || part.Stop < part.Start || part.Stop > imap.UID(last) || uint64(part.Stop)-uint64(part.Start)+1 > uint64(4096-len(ids)) {
			return nil, 0, ErrScope
		}
		for uid := uint64(part.Start); uid <= uint64(part.Stop); uid++ {
			ids = append(ids, imap.UID(uid))
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return nil, 0, ErrScope
		}
	}
	through := last
	if len(ids) >= limit {
		ids = ids[:limit]
		through = uint32(ids[len(ids)-1])
	}
	return ids, through, nil
}
