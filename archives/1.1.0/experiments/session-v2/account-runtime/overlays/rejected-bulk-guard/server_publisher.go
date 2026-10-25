package transport

import (
	"context"
	"crypto/rand"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/wire"
)

// ServerPublisher owns only SERVER-origin service. Its local journal and the
// client's remote-grant wallet are different authorities even on one mailbox.
type ServerPublisher struct {
	session           *Session
	plane             Plane
	class             LedgerClass
	cipher            *Cipher
	store             *journal.Store
	scope             journal.Scope
	folder            string
	authenticationKey [32]byte
}

func NewServerPublisher(s *Session, plane Plane, class LedgerClass, cipher *Cipher, store *journal.Store, scope journal.Scope, folder string, key []byte) (*ServerPublisher, error) {
	if s == nil || plane > Bulk || class != DataLedger && class != ControlLedger || class == ControlLedger && plane != Fast || cipher == nil || cipher.epoch == nil || store == nil || folder == "" || len(key) != 32 {
		return nil, ErrProtocol
	}
	binding := cipher.epoch.Context()
	if scope.Account == "" || scope.User != binding.User || scope.Session != binding.Session {
		return nil, ErrProtocol
	}
	if err := s.bindCipher(cipher, wire.S2C); err != nil {
		return nil, err
	}
	p := &ServerPublisher{session: s, plane: plane, class: class, cipher: cipher, store: store, scope: scope, folder: folder}
	copy(p.authenticationKey[:], key)
	return p, nil
}

func (p *ServerPublisher) Prepare(ctx context.Context, maxPayload int) (*PreparedPublication, error) {
	if maxPayload < 0 || maxPayload > MaxPayload {
		return nil, ErrCapacity
	}
	return p.prepare(ctx, maxPayload, false, false)
}
func (p *ServerPublisher) PrepareBulk(ctx context.Context, maxEncryptedBytes int) (*PreparedPublication, error) {
	if p.plane != Bulk || p.class != DataLedger || maxEncryptedBytes > MaxBulkBytes {
		return nil, ErrProtocol
	}
	overhead, _ := secure.SessionPacketSize(headerSize)
	if maxEncryptedBytes < bulkHeader+bulkEntryHeader+overhead+1 {
		return nil, ErrCapacity
	}
	return p.prepare(ctx, maxEncryptedBytes, true, false)
}

func (p *ServerPublisher) PrepareControls(ctx context.Context, maxEncryptedBytes int) (*PreparedPublication, error) {
	overhead, _ := secure.SessionPacketSize(headerSize)
	if p.plane != Fast || p.class != ControlLedger || maxEncryptedBytes > MaxControlBytes {
		return nil, ErrProtocol
	}
	if maxEncryptedBytes < overhead {
		return nil, ErrCapacity
	}
	return p.prepare(ctx, maxEncryptedBytes, false, true)
}

func (p *ServerPublisher) prepare(ctx context.Context, maximum int, bulk, controls bool) (*PreparedPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	minimum, ready := p.session.minimumPayloadFor(p.plane, p.class)
	if !ready {
		return nil, nil
	}
	if bulk {
		overhead, _ := secure.SessionPacketSize(headerSize)
		minimum += bulkHeader + bulkEntryHeader + overhead
	}
	if controls {
		overhead, _ := secure.SessionPacketSize(headerSize)
		minimum += overhead
	}
	if maximum < minimum {
		return nil, ErrCapacity
	}
	if !p.session.ReadyFor(p.plane, p.class) {
		return nil, nil
	}
	{
		release, ok := p.session.claimPreparation(p.plane, p.class, maximum, bulk)
		if !ok {
			return nil, nil
		}
		defer release()
	}
	class := wire.ServerData
	if p.class == ControlLedger {
		class = wire.ServerControl
	}
	// Grant IDs and counters are fixed width. Predict with a valid placeholder;
	// only the concrete ticket's key can be bound into the final physical BODY.
	shape := wire.QuotaKey{Store: p.store.StoreID(), Grant: 1, Epoch: p.store.Run(), Session: p.scope.Session, Account: p.scope.Account, User: p.scope.User}
	size := func(n int) (uint64, error) {
		packetBytes := n
		if !bulk && !controls {
			var err error
			packetBytes, err = secure.SessionPacketSize(headerSize + n)
			if err != nil {
				return 0, err
			}
		}
		body, err := wire.ServerEnvelopeSize(shape, class, p.folder, packetBytes)
		if err != nil {
			return 0, err
		}
		return mailbox.EncodedSize(body)
	}
	ticket, limit, err := p.store.ReserveLocalSized(p.scope, p.folder, minimum, maximum, size)
	if err != nil {
		return nil, err
	}
	var work publicationWork
	var packet []byte
	if bulk || controls {
		var batch *DispatchBatch
		var ok bool
		if controls {
			batch, ok = p.session.TakeControls(limit)
		} else {
			batch, ok = p.session.TakeBulk(limit)
		}
		if !ok {
			return nil, ticket.Cancel()
		}
		work = batch
		records := batch.Records()
		if controls {
			packet, err = p.cipher.SealControls(records)
		} else {
			packet, err = p.cipher.SealRecords(records)
		}
		for _, r := range records {
			clear(r.Payload)
		}
		if err == nil && len(packet) != batch.EncryptedBytes() {
			err = ErrProtocol
		}
	} else {
		dispatch, ok := p.session.TakeFor(p.plane, limit, p.class)
		if !ok {
			return nil, ticket.Cancel()
		}
		work = dispatch
		packet, err = p.cipher.Seal(dispatch.Record)
	}
	defer clear(packet)
	unstarted := func(reason error) (*PreparedPublication, error) {
		return nil, errors.Join(reason, work.complete(NotStarted, false), ticket.Cancel())
	}
	if err != nil {
		return unstarted(err)
	}
	var batch wire.ID
	if _, err = rand.Read(batch[:]); err != nil {
		return unstarted(err)
	}
	usage, body, err := wire.BindServerEnvelope(ticket.Key(), class, batch, p.folder, packet, p.authenticationKey[:], func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
	if err != nil {
		return unstarted(err)
	}
	attempt, err := ticket.Commit(batch, usage.Attempt.EncodedBytes, body)
	if err != nil {
		clear(body)
		return unstarted(err) // NO I/O allowed; any ambiguous journal debt stays held.
	}
	prepared, err := finishPublication(work, usage, body, p.folder)
	if err != nil {
		return nil, err
	}
	prepared.local = &localPublication{store: p.store, attempt: attempt.Ref}
	return prepared, nil
}
