package transport

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"sync"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// Bootstrap work has no application stream. Its exact packet remains in the
// bootstrap owner's pending publication; completion grants no data receipt,
// memory release or quota refund. Only trusted storage proof resolves unknown.
type bootstrapWork struct {
	mu        sync.Mutex
	uncertain bool
}

func (w *bootstrapWork) complete(outcome PublicationOutcome, proof bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if outcome > Uncertain || w.uncertain && (!proof || outcome != Accepted) {
		return ErrProtocol
	}
	w.uncertain = outcome == Uncertain
	return nil
}

// Prepare reserves an exact existing CONTROL grant before Noise.Write/Seal and
// commits the original charge before any I/O. Waiting for a peer, missing quota,
// or an unknown command never creates another bootstrap message or permission.
func (b *SessionBootstrap) Prepare(ctx context.Context) (*PreparedPublication, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.failed || b.handedOff {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.pending != nil {
		b.pending.mu.Lock()
		started, finished, outcome := b.pending.started, b.pending.finished, b.pending.outcome
		b.pending.mu.Unlock()
		if !started && !finished {
			return b.pending, nil
		}
		if !finished {
			return nil, ErrBootstrapPending
		}
		if outcome != Accepted {
			return nil, ErrUnknown
		}
		if b.complete {
			return nil, nil
		}
		b.pending = nil
	}
	if b.send == 0 {
		return nil, ErrBootstrapPending
	}
	kind := b.send
	size := bootstrapHeader + bootstrapPayloadSize(kind)
	var prepare func([]byte) (*PreparedPublication, error)
	var cancel func() error
	if b.client {
		ticket, _, err := b.wallet.ReserveSized(b.origin.Account, size, size, func(key wire.QuotaKey, n int) (uint64, error) {
			if key.Store != b.origin.Store || key.Epoch != b.origin.Run || key.Session != b.binding.Session || key.User != b.binding.User || key.Account != b.origin.Account {
				return 0, ErrProtocol
			}
			body, err := wire.DataEnvelopeSize(key, b.origin.Folder, n)
			if err != nil {
				return 0, err
			}
			return mailbox.EncodedSize(body)
		})
		if err != nil {
			return nil, err
		}
		cancel = func() error { ticket.Cancel(); return nil }
		prepare = func(packet []byte) (*PreparedPublication, error) {
			usage, body, err := ticket.Commit(b.origin.Folder, packet, b.key[:], func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
			if err != nil {
				clear(body)
				return nil, err
			}
			return finishPublication(&bootstrapWork{}, usage, body, b.origin.Folder)
		}
	} else {
		shape := wire.QuotaKey{Store: b.origin.Store, Grant: 1, Epoch: b.origin.Run, Session: b.binding.Session, Account: b.origin.Account, User: b.binding.User}
		scope := journal.Scope{Account: b.origin.Account, User: b.binding.User, Session: b.binding.Session}
		ticket, _, err := b.store.ReserveLocalSized(scope, b.origin.Folder, size, size, func(n int) (uint64, error) {
			body, err := wire.ServerEnvelopeSize(shape, wire.ServerControl, b.origin.Folder, n)
			if err != nil {
				return 0, err
			}
			return mailbox.EncodedSize(body)
		})
		if err != nil {
			return nil, err
		}
		cancel = ticket.Cancel
		prepare = func(packet []byte) (*PreparedPublication, error) {
			var batch wire.ID
			if _, err := rand.Read(batch[:]); err != nil {
				return nil, err
			}
			usage, body, err := wire.BindServerEnvelope(ticket.Key(), wire.ServerControl, batch, b.origin.Folder, packet, b.key[:], func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
			if err != nil {
				return nil, err
			}
			attempt, err := ticket.Commit(batch, usage.Attempt.EncodedBytes, body)
			if err != nil {
				clear(body)
				return nil, err
			}
			p, err := finishPublication(&bootstrapWork{}, usage, body, b.origin.Folder)
			if err != nil {
				return nil, err
			}
			p.local = &localPublication{store: b.store, attempt: attempt.Ref}
			return p, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, cancel())
	}
	payload, err := b.preparePayloadLocked(kind)
	if err != nil {
		b.failed = true
		return nil, errors.Join(err, cancel())
	}
	frame := bootstrapFrame{kind: kind, binding: b.binding, generation: b.generation, payload: payload}
	packet, err := frame.encode()
	clear(payload)
	if err != nil {
		b.failed = true
		return nil, errors.Join(err, cancel())
	}
	defer clear(packet)
	if kind == bootstrapRequest {
		b.request = append([]byte(nil), packet...)
	}
	if kind == bootstrapResponse {
		b.response = append([]byte(nil), packet...)
		if err = b.bindEpochLocked(); err != nil {
			b.failed = true
			return nil, errors.Join(err, cancel())
		}
	}
	p, err := prepare(packet)
	if err != nil {
		b.failed = true
		return nil, errors.Join(err, cancel())
	}
	b.pending, b.send = p, 0
	b.own[kind] = sha256.Sum256(packet)
	if kind == bootstrapServerAck {
		b.complete = true
	} else {
		b.receive = kind + 1
	}
	return p, nil
}
