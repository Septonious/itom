package transport

import (
	"context"
	"crypto/sha256"
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/wallet"
	"itom.local/core/wire"
	"sync"
)

// ClientPublisher couples the new scheduler to existing durable client quota
// authority and canonical MIME only. It does not use mailbox.Client's command
// scheduling. DATA and protocol publishers must bind their own ledger stores;
// sharing a Fast mailbox does not allow borrowing either allowance.
type ClientPublisher struct {
	session           *Session
	plane             Plane
	class             LedgerClass
	cipher            *Cipher
	wallet            *wallet.Wallet
	account, folder   string
	authenticationKey [32]byte
}

func NewClientPublisher(s *Session, plane Plane, class LedgerClass, cipher *Cipher, w *wallet.Wallet, account, folder string, authenticationKey []byte) (*ClientPublisher, error) {
	if class != DataLedger && class != ControlLedger || class == ControlLedger && plane != Fast || s == nil || plane > Bulk || cipher == nil || cipher.epoch == nil || w == nil || account == "" || folder == "" || len(authenticationKey) != 32 {
		return nil, ErrProtocol
	}
	identity, binding := w.Identity(), cipher.epoch.Context()
	if identity.User != binding.User || identity.Session != binding.Session {
		return nil, ErrProtocol
	}
	if err := s.bindCipher(cipher, wire.C2S); err != nil {
		return nil, err
	}
	p := &ClientPublisher{session: s, plane: plane, class: class, cipher: cipher, wallet: w, account: account, folder: folder}
	copy(p.authenticationKey[:], authenticationKey)
	return p, nil
}

// PreparedPublication owns one durably charged exact physical body. A command
// canceled before it starts can be started later with these SAME immutable
// bytes and permit. Once started it can never be blindly re-APPENDed, including
// after a lost reply or an ordinary application cancellation.
type PreparedPublication struct {
	mu                sync.Mutex
	work              publicationWork
	usage             wire.UsageReport
	mime              []byte
	folder            string
	started, finished bool
	outcome           PublicationOutcome
	err               error
	local             *localPublication
}

type localPublication struct {
	store   *journal.Store
	attempt journal.AttemptRef
}

func (p *ClientPublisher) Prepare(ctx context.Context, maxPayload int) (*PreparedPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxPayload < 0 || maxPayload > MaxPayload {
		return nil, ErrCapacity
	}
	if !p.session.ReadyFor(p.plane, p.class) {
		return nil, nil
	}
	size := func(key wire.QuotaKey, n int) (uint64, error) {
		packet, err := secure.SessionPacketSize(headerSize + n)
		if err != nil {
			return 0, err
		}
		body, err := wire.DataEnvelopeSize(key, p.folder, packet)
		if err != nil {
			return 0, err
		}
		return mailbox.EncodedSize(body)
	}
	minimum, ready := p.session.minimumPayloadFor(p.plane, p.class)
	if !ready {
		return nil, nil
	}
	if maxPayload < minimum {
		return nil, ErrCapacity
	}
	ticket, limit, err := p.wallet.ReserveSized(p.account, minimum, maxPayload, size)
	if err != nil {
		return nil, err
	}
	dispatch, ok := p.session.TakeFor(p.plane, limit, p.class)
	if !ok {
		ticket.Cancel()
		return nil, nil
	}
	packet, err := p.cipher.Seal(dispatch.Record)
	if err != nil {
		ticket.Cancel()
		_ = dispatch.Complete(NotStarted)
		return nil, err
	}
	usage, body, err := ticket.Commit(p.folder, packet, p.authenticationKey[:], func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
	clear(packet)
	if err != nil {
		ticket.Cancel()
		_ = dispatch.Complete(NotStarted)
		return nil, err
	}
	return finishPublication(dispatch, usage, body, p.folder)
}

func finishPublication(work publicationWork, usage wire.UsageReport, body []byte, folder string) (*PreparedPublication, error) {
	mime, err := mailbox.Encode(body)
	clear(body)
	if err != nil || usage.Attempt == nil || uint64(len(mime)) != usage.Attempt.EncodedBytes {
		// The durable spend has happened. It cannot be canceled/refunded locally.
		// Fail closed; keep the logical publication uncertain for reconciliation.
		_ = work.complete(Uncertain, false)
		return nil, ErrProtocol
	}
	return &PreparedPublication{work: work, usage: usage, mime: mime, folder: folder}, nil
}

// Start's context governs admission to the executor, not the lifetime of an
// already issued literal. The executor has its own bounded completion timeout.
func (p *PreparedPublication) Start(ctx context.Context, executor *Executor) (PublicationOutcome, error) {
	if p == nil || executor == nil || executor.account == "" || executor.account != p.usage.Key.Account || executor.mailbox != p.folder || executor.validity == 0 {
		return NotStarted, ErrProtocol
	}
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return NotStarted, ErrProtocol
	}
	p.started = true
	p.mu.Unlock()
	command, err := executor.Append(ctx, p.folder, p.mime)
	if command == nil {
		// No command was issued and the durable permit is still unused. Retain it
		// for its owner rather than creating a second spend or losing its bytes.
		p.mu.Lock()
		p.started = false
		p.mu.Unlock()
		return NotStarted, err
	}
	if err == nil {
		_, err = command.Wait(context.Background())
	}
	outcome := Accepted
	if err != nil {
		outcome = Uncertain
	}
	if p.local != nil {
		result := journal.Accepted
		if outcome == Uncertain {
			result = journal.Uncertain
		}
		if commitErr := p.local.store.Complete(p.local.attempt, result); commitErr != nil {
			outcome = Uncertain
			err = errors.Join(err, commitErr)
		}
	}
	// Even a definitive provider refusal stays charged in the wallet until the
	// original grant/proof authority reconciles it. No local debt reset exists.
	completion := p.work.complete(outcome, false)
	p.mu.Lock()
	p.finished = true
	p.outcome = outcome
	p.err = err
	p.mu.Unlock()
	if err != nil {
		return outcome, err
	}
	if completion != nil && !errors.Is(completion, ErrClosed) {
		return outcome, completion
	}
	return outcome, nil
}
func (p *PreparedPublication) Usage() wire.UsageReport {
	usage := p.usage
	if usage.Attempt != nil {
		copy := *usage.Attempt
		usage.Attempt = &copy
	}
	return usage
}

// MIME returns a copy for a bounded recovery/audit worker. Seeing one's own
// physical body is not peer delivery and must not advance session receipts.
func (p *PreparedPublication) MIME() []byte { return append([]byte(nil), p.mime...) }

// ServerAttempt exposes local recovery identity only. Its descriptor must never
// be treated as a client's UsageReport or permission to retire remote debt.
func (p *PreparedPublication) ServerAttempt() (journal.AttemptRef, bool) {
	if p == nil || p.local == nil {
		return journal.AttemptRef{}, false
	}
	return p.local.attempt, true
}

// ConfirmStored is a trusted local FETCH check, not a peer report or a refund.
// It can resolve only provider acceptance of these exact prepared bytes. It
// never synthesizes session receipt, delivery bandwidth or reclaimed quota.
func (p *PreparedPublication) ConfirmStored(ctx context.Context, executor *Executor, uid uint32) error {
	if p == nil || executor == nil || uid == 0 || executor.validity == 0 || executor.mailbox != p.folder || executor.account == "" || executor.account != p.usage.Key.Account {
		return ErrProtocol
	}
	p.mu.Lock()
	eligible := p.finished && p.outcome == Uncertain
	p.mu.Unlock()
	if !eligible {
		return ErrProtocol
	}
	command, err := executor.Fetch(ctx, uid, uid)
	if err != nil {
		return err
	}
	bodies, err := command.Wait(ctx)
	if err != nil {
		return err
	}
	if len(bodies) != 1 || bodies[0].UID != uid {
		return ErrProtocol
	}
	charge, err := mailbox.CanonicalEncodedBytes(bodies[0].Bytes)
	if err != nil {
		return err
	}
	body, err := mailbox.Decode(bodies[0].Bytes)
	if err != nil {
		return err
	}
	defer clear(body)
	if p.usage.Attempt == nil || charge != p.usage.Attempt.EncodedBytes || sha256.Sum256(body) != p.usage.Attempt.Digest {
		return ErrProtocol
	}
	if p.local != nil {
		proof := journal.Observation{Account: p.local.attempt.Grant.Scope.Account, Folder: p.folder, EncodedBytes: charge, Location: journal.Location{UIDValidity: executor.validity, UID: uid}, Decoded: body}
		if err = p.local.store.Observe(p.local.attempt, p.local.store.Run(), proof); err != nil {
			return err
		}
	}
	err = p.work.complete(Accepted, true)
	if err != nil && !errors.Is(err, ErrClosed) {
		return err
	}
	p.mu.Lock()
	p.outcome = Accepted
	p.err = nil
	p.mu.Unlock()
	return nil
}

// PrepareBulk fits a whole DATA container to one concrete grant before draining
// or encrypting records. maxEncryptedBytes is a service/memory bound, not a
// minimum fill size, so fresh demand does not wait to collect a larger BODY.
func (p *ClientPublisher) PrepareBulk(ctx context.Context, maxEncryptedBytes int) (*PreparedPublication, error) {
	return p.prepareBatch(ctx, maxEncryptedBytes, false)
}

func (p *ClientPublisher) PrepareControls(ctx context.Context, maxEncryptedBytes int) (*PreparedPublication, error) {
	return p.prepareBatch(ctx, maxEncryptedBytes, true)
}

func (p *ClientPublisher) prepareBatch(ctx context.Context, maxEncryptedBytes int, controls bool) (*PreparedPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !controls && (p.plane != Bulk || p.class != DataLedger || maxEncryptedBytes > MaxBulkBytes) || controls && (p.plane != Fast || p.class != ControlLedger || maxEncryptedBytes > MaxControlBytes) {
		return nil, ErrProtocol
	}
	overhead, _ := secure.SessionPacketSize(headerSize)
	minimum := bulkHeader + bulkEntryHeader + overhead + 1
	if controls {
		minimum = overhead
	}
	if maxEncryptedBytes < minimum {
		return nil, ErrCapacity
	}
	payloadMinimum, ready := p.session.minimumPayloadFor(p.plane, p.class)
	if !ready {
		return nil, nil
	}
	minimum = bulkHeader + bulkEntryHeader + overhead + payloadMinimum
	if controls {
		minimum = overhead + payloadMinimum
	}
	if maxEncryptedBytes < minimum {
		return nil, ErrCapacity
	}
	if !p.session.ReadyFor(p.plane, p.class) {
		return nil, nil
	}
	size := func(key wire.QuotaKey, encryptedBytes int) (uint64, error) {
		body, err := wire.DataEnvelopeSize(key, p.folder, encryptedBytes)
		if err != nil {
			return 0, err
		}
		return mailbox.EncodedSize(body)
	}
	ticket, limit, err := p.wallet.ReserveSized(p.account, minimum, maxEncryptedBytes, size)
	if err != nil {
		return nil, err
	}
	var batch *DispatchBatch
	var ok bool
	if controls {
		batch, ok = p.session.TakeControls(limit)
	} else {
		batch, ok = p.session.TakeBulk(limit)
	}
	if !ok {
		ticket.Cancel()
		return nil, nil
	}
	records := batch.Records()
	var packet []byte
	if controls {
		packet, err = p.cipher.SealControls(records)
	} else {
		packet, err = p.cipher.SealRecords(records)
	}
	for _, record := range records {
		clear(record.Payload)
	}
	if err != nil {
		ticket.Cancel()
		_ = batch.Complete(NotStarted)
		return nil, err
	}
	if len(packet) != batch.EncryptedBytes() {
		ticket.Cancel()
		_ = batch.Complete(NotStarted)
		clear(packet)
		return nil, ErrProtocol
	}
	usage, body, err := ticket.Commit(p.folder, packet, p.authenticationKey[:], func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) })
	clear(packet)
	if err != nil {
		ticket.Cancel()
		_ = batch.Complete(NotStarted)
		return nil, err
	}
	return finishPublication(batch, usage, body, p.folder)
}
