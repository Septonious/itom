package mailbox

import (
	"context"
	"errors"
	"sync"
)

var ErrPending = errors.New("IMAP_DELIVERY_PENDING")

// Delivery transfers one raw, untrusted message to an owner. Token is local to
// this inbox instance, not an on-wire acknowledgment or a physical cleanup ID.
type DeliveryToken struct {
	owner    *Inbox
	sequence uint64
}

type Delivery struct {
	Token   DeliveryToken
	Message Message
}

// Inbox holds at most one Page (<=16 messages, each <=MaxMIME). It does not
// decrypt, delete, or advance past a delivery until the owner accepts it into
// its own bounded state. A missing remote DATA descriptor must be handled by a
// bounded owner spool, not by discarding DATA or blocking control discovery.
type Inbox struct {
	mu             sync.Mutex
	mail           Transport
	folder         string
	cursor, next   Cursor
	page           []Message
	index          int
	token, pending uint64
}

func NewInbox(mail Transport, folder string, cursor Cursor) (*Inbox, error) {
	if mail == nil || !validFolder(folder) {
		return nil, ErrScope
	}
	return &Inbox{mail: mail, folder: folder, cursor: cursor}, nil
}

// Next returns nil when caught up. It never polls or retries behind the caller's
// back. On I/O failure the cursor is unchanged. Calls must be serialized by one
// read worker; Accept can be called by the owner after it takes responsibility.
func (i *Inbox) Next(ctx context.Context) (*Delivery, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.pending != 0 {
		return nil, ErrPending
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if i.index == len(i.page) {
		rows, next, err := i.mail.Page(ctx, i.folder, i.cursor)
		if err != nil {
			return nil, err
		}
		if len(rows) > 16 {
			return nil, ErrLimit
		}
		seen := map[uint32]bool{}
		if next.Validity == 0 || next.Validity == i.cursor.Validity && next.After < i.cursor.After {
			return nil, ErrScope
		}
		for _, m := range rows {
			if m.Folder != i.folder || m.UID == 0 || m.Validity != next.Validity || m.UID > next.After || seen[m.UID] || m.Validity == i.cursor.Validity && m.UID <= i.cursor.After || len(m.MIME) == 0 || len(m.MIME) > MaxMIME {
				return nil, ErrScope
			}
			seen[m.UID] = true
		}
		i.page, i.next, i.index = rows, next, 0
		if len(rows) == 0 {
			i.cursor = next
			return nil, nil
		}
	}
	if i.token == ^uint64(0) {
		return nil, ErrLimit
	}
	i.token++
	i.pending = i.token
	return &Delivery{Token: DeliveryToken{i, i.token}, Message: i.page[i.index]}, nil
}

// Accept means ownership transfer, not authentication/delivery/GC. The owner
// must preserve the raw message or its successfully authenticated frame until
// applied. For invalid/foreign mail, an explicit decision to discard suffices.
func (i *Inbox) Accept(token DeliveryToken) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if token.owner != i || token.sequence == 0 || token.sequence != i.pending {
		return ErrScope
	}
	i.page[i.index] = Message{}
	i.index++
	i.pending = 0
	if i.index == len(i.page) {
		i.cursor = i.next
		i.page = nil
		i.index = 0
	}
	return nil
}
func (i *Inbox) Cursor() Cursor { i.mu.Lock(); defer i.mu.Unlock(); return i.cursor }
