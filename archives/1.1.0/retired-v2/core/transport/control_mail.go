package transport

import (
	"context"
	"crypto/rand"
	"errors"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// prepareControlPacket is for pre-Noise metadata, not uncharged bootstrap mail.
// The one-body local ticket and physical intent are committed by the original
// CONTROL journal before this can return a publication permit.
func prepareControlPacket(ctx context.Context, store *journal.Store, origin ServerOrigin, session wire.ID, packet, key []byte) (*PreparedPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	shape := wire.QuotaKey{Store: origin.Store, Grant: 1, Epoch: origin.Run, Session: session, Account: origin.Account, User: origin.User}
	scope := journal.Scope{Account: origin.Account, User: origin.User, Session: session}
	ticket, _, err := store.ReserveLocalSized(scope, origin.Folder, len(packet), len(packet), func(n int) (uint64, error) {
		body, err := wire.ServerEnvelopeSize(shape, wire.ServerControl, origin.Folder, n)
		if err != nil {
			return 0, err
		}
		return mailbox.EncodedSize(body)
	})
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, errors.Join(err, ticket.Cancel())
	}
	p, err := commitControlPacket(ticket, store, origin.Folder, packet, key)
	if err != nil {
		return nil, errors.Join(err, ticket.Cancel())
	}
	return p, nil
}

func commitControlPacket(ticket *journal.LocalTicket, store *journal.Store, folder string, packet, key []byte) (*PreparedPublication, error) {
	var batch wire.ID
	if _, err := rand.Read(batch[:]); err != nil {
		return nil, err
	}
	usage, body, err := wire.BindServerEnvelope(ticket.Key(), wire.ServerControl, batch, folder, packet, key, func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
	if err != nil {
		return nil, err
	}
	attempt, err := ticket.Commit(batch, usage.Attempt.EncodedBytes, body)
	if err != nil {
		clear(body)
		return nil, err
	}
	p, err := finishPublication(&bootstrapWork{}, usage, body, folder)
	if err != nil {
		return nil, err
	}
	p.local = &localPublication{store: store, attempt: attempt.Ref}
	return p, nil
}
