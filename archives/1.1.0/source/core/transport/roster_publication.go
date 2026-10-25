package transport

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/wire"
)

// Reserve the original CONTROL permission before allocating a Noise nonce.
// A pending or uncertain write remains the same charged publication.
func (d *RosterDriver) prepare(ctx context.Context) (*PreparedPublication, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.failed {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.pending != nil {
		d.pending.mu.Lock()
		started, finished, outcome := d.pending.started, d.pending.finished, d.pending.outcome
		d.pending.mu.Unlock()
		if !started && !finished {
			return d.pending, nil
		}
		if !finished {
			return nil, ErrBootstrapPending
		}
		if outcome != Accepted {
			return nil, ErrUnknown
		}
		d.pending = nil
	}
	if d.send == 0 {
		return nil, ErrBootstrapPending
	}
	stage := d.send
	body := d.digest[:]
	if stage == 1 {
		body = rosterChunk(d.encoded, d.sentOffset, d.digest)
		defer clear(body)
	}
	size, err := secure.SessionPacketSize(9 + len(body))
	if err != nil {
		return nil, err
	}
	size += 4
	origin := d.input.origin
	binding := d.input.epoch.Context()
	var commit func([]byte) (*PreparedPublication, error)
	var cancel func() error
	if d.client {
		ticket, _, err := d.wallet.ReserveSized(origin.Account, size, size, func(key wire.QuotaKey, n int) (uint64, error) {
			if key.Store != origin.Store || key.Epoch != origin.Run || key.Session != binding.Session || key.User != binding.User || key.Account != origin.Account {
				return 0, ErrProtocol
			}
			bytes, err := wire.DataEnvelopeSize(key, origin.Folder, n)
			if err != nil {
				return 0, err
			}
			return mailbox.EncodedSize(bytes)
		})
		if err != nil {
			return nil, err
		}
		cancel = func() error { ticket.Cancel(); return nil }
		commit = func(packet []byte) (*PreparedPublication, error) {
			usage, body, err := ticket.Commit(origin.Folder, packet, d.key[:], func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
			if err != nil {
				clear(body)
				return nil, err
			}
			return finishPublication(&bootstrapWork{}, usage, body, origin.Folder)
		}
	} else {
		shape := wire.QuotaKey{Store: origin.Store, Grant: 1, Epoch: origin.Run, Session: binding.Session, Account: origin.Account, User: binding.User}
		scope := journal.Scope{Account: origin.Account, User: binding.User, Session: binding.Session}
		ticket, _, err := d.store.ReserveLocalSized(scope, origin.Folder, size, size, func(n int) (uint64, error) {
			bytes, err := wire.ServerEnvelopeSize(shape, wire.ServerControl, origin.Folder, n)
			if err != nil {
				return 0, err
			}
			return mailbox.EncodedSize(bytes)
		})
		if err != nil {
			return nil, err
		}
		cancel = ticket.Cancel
		commit = func(packet []byte) (*PreparedPublication, error) {
			return commitControlPacket(ticket, d.store, origin.Folder, packet, d.key[:])
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, cancel())
	}
	plain := make([]byte, 9+len(body))
	plain[0] = stage
	binary.BigEndian.PutUint64(plain[1:9], d.input.generation)
	copy(plain[9:], body)
	sealed, err := d.input.epoch.SealSession(plain, true)
	clear(plain)
	if err != nil {
		d.failed = true
		return nil, errors.Join(err, cancel())
	}
	packet := append([]byte("IRO2"), sealed...)
	clear(sealed)
	defer clear(packet)
	p, err := commit(packet)
	if err != nil {
		d.failed = true
		return nil, errors.Join(err, cancel())
	}
	d.pending, d.send = p, 0
	d.own[sha256.Sum256(packet)] = stage
	d.status.Prepared++
	if stage == 1 {
		d.sentOffset += len(body) - rosterChunkHeader
		if d.sentOffset < len(d.encoded) {
			d.send = 1
		} else {
			d.receive = 2
		}
	} else if stage < 3 {
		d.receive = stage + 1
	}
	return p, nil
}
