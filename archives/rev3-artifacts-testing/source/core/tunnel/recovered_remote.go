package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Discovery only: no registration, plaintext delivery, peer ACK or quota refund
// happens until a descriptor arrives over the authenticated fresh control path.
func (l *lane) queuePendingRemote(m mailbox.Message, body []byte) error {
	legacy := bytes.HasPrefix(body, []byte("ITQ1")) && len(body) <= wire.MaxDataBatchBody+256
	envelope := bytes.HasPrefix(body, []byte(wire.DataEnvelopeMagic)) && len(body) <= wire.MaxDataEnvelopeBody
	if !legacy && !envelope {
		return nil
	}
	if !l.side || !l.ownsReceiveFolder(m.Folder) || m.Validity == 0 || m.UID == 0 {
		return journal.ErrScope
	}
	digest := sha256.Sum256(body)
	if _, duplicate := l.pending[digest]; duplicate {
		return nil
	}
	pendingBytes := len(m.MIME)
	for _, queued := range l.pending {
		pendingBytes += len(queued.MIME)
	}
	if len(l.pending) >= 128 || pendingBytes > pendingDataMIMEHardCap {
		return errors.New("PENDING_DATA_LIMIT")
	}
	l.pending[digest] = m
	return nil
}

func (l *lane) retainRecoveredRemote(rows []mailbox.Message, removed []journal.Attempt) error {
	deleted := make(map[[32]byte]bool, len(removed))
	for _, a := range removed {
		deleted[a.Digest] = true
	}
	for _, row := range rows {
		body, err := mailbox.Decode(row.MIME)
		if err != nil || deleted[sha256.Sum256(body)] {
			continue
		}
		if err := l.queuePendingRemote(row, body); err != nil {
			return err
		}
	}
	return nil
}

// A registered prefix may already have been ACKed to the wallet, which then
// legitimately prunes its pending descriptor. Recover these old attempts from
// the server's durable journal instead of relying on another wallet replay.
// This queues discovery only; it cannot acknowledge/refund an unseen BODY.
func (l *lane) queueRegisteredRemoteRecovery() error {
	attempts, err := allAttempts(l.data)
	if err != nil {
		return err
	}
	for _, a := range attempts {
		if a.Ordinal == 0 || !l.ownsReceiveFolder(a.Folder) || a.Ref.Grant.Scope.Session == l.cipher.Context().Session {
			continue
		}
		status, err := l.data.RemoteStatus(a.Ref.Grant)
		if err != nil {
			return err
		}
		raw := l.pending[a.Digest]
		delete(l.pending, a.Digest)
		l.remoteGC[a.Ref] = remoteCleanup{message: raw, key: status.Key, digest: a.Digest}
	}
	return nil
}

// One bounded recovery page per cleanup turn. Only authenticated old-session
// descriptors supply wanted digests. A missed body or an exhausted cursor is
// never an absence/deletion proof; the next sweep retries from the beginning.
func (l *lane) discoverRecoveredRemote(remote map[journal.AttemptRef]remoteCleanup, cursor mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error) {
	wanted := map[[32]byte]bool{}
	for _, entry := range remote {
		if entry.message.UID == 0 && entry.key.Session != l.cipher.Context().Session && entry.digest != ([32]byte{}) {
			wanted[entry.digest] = true
		}
	}
	if len(wanted) == 0 {
		return nil, cursor, nil
	}
	page := l.mail.Page
	if recovery, ok := l.mail.(interface {
		PageRecovery(context.Context, string, mailbox.Cursor) ([]mailbox.Message, mailbox.Cursor, error)
	}); ok {
		page = recovery.PageRecovery
	}
	folder := l.recvFolder
	attempts, err := allAttempts(l.data)
	if err != nil {
		return nil, cursor, err
	}
	for _, a := range attempts {
		if wanted[a.Digest] && l.ownsReceiveFolder(a.Folder) {
			folder = a.Folder
			break
		}
	}
	rows, next, err := page(l.ctx, folder, cursor)
	if errors.Is(err, mailbox.ErrChanged) {
		return nil, mailbox.Cursor{}, nil
	}
	if err != nil {
		return nil, cursor, err
	}
	if next == cursor {
		next = mailbox.Cursor{}
	}
	kept := rows[:0]
	for _, row := range rows {
		body, err := mailbox.Decode(row.MIME)
		if err == nil && row.Folder == folder && row.Validity != 0 && row.UID != 0 && wanted[sha256.Sum256(body)] {
			kept = append(kept, row)
		}
	}
	return kept, next, nil
}
