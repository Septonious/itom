// Package mailcontrol writes authenticated control records through a dedicated
// durable journal budget. DATA is rejected; control capacity cannot fund DATA.
package mailcontrol

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"sync"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/pool"
	"itom.local/core/secure"
	"itom.local/core/wire"
)

var ErrScope = errors.New("CONTROL_MAIL_SCOPE")
var ErrStopped = errors.New("CONTROL_MAIL_STOPPED")

const MaxRecord = 2*wire.HeaderSize + wire.FrameHeaderSize + wire.MaxControl + wire.TagSize

// SendResult always exposes the durable attempt once Prepare succeeds, even
// when APPEND completion is lost. Such an attempt must never be sent again.
type SendResult struct {
	Attempt *journal.AttemptRef
	Err     error
}
type Sender struct {
	mu      sync.Mutex
	store   *journal.Store
	scope   journal.Scope
	folder  string
	cipher  *secure.Epoch
	mail    mailbox.Transport
	stopped bool
}

// The store must be a separately provisioned control reserve, not the DATA
// ledger. The owner inventories it and enables issuance before constructing us.
func NewSender(store *journal.Store, scope journal.Scope, folder string, cipher *secure.Epoch, mail mailbox.Transport) (*Sender, error) {
	if store == nil || cipher == nil || mail == nil || scope.Account == "" || folder == "" {
		return nil, ErrScope
	}
	b := cipher.Context()
	if scope.User != b.User || scope.Session != b.Session {
		return nil, ErrScope
	}
	return &Sender{store: store, scope: scope, folder: folder, cipher: cipher, mail: mail}, nil
}
func (s *Sender) Stop() { s.mu.Lock(); defer s.mu.Unlock(); s.stopped = true }

// Send is synchronous and serialized. The caller invokes it on a worker, never
// on the session actor. Retransmission decisions belong to the state machine:
// each new Send spends a fresh nonce and a fresh journal attempt.
func (s *Sender) Send(ctx context.Context, f wire.Frame) (result SendResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		result.Err = ErrStopped
		return
	}
	if result.Err = ctx.Err(); result.Err != nil {
		return
	}
	if _, result.Err = wire.DecodeControl(f); result.Err != nil {
		return
	}
	packet, err := s.cipher.Seal(f)
	if err != nil {
		result.Err = err
		return
	}
	defer clear(packet)
	body := append([]byte("ITC1"), packet...)
	defer clear(body)
	raw, err := mailbox.Encode(body)
	if err != nil {
		result.Err = err
		return
	}
	defer clear(raw)
	var batch wire.ID
	if _, err = rand.Read(batch[:]); err != nil {
		result.Err = err
		return
	}
	grant, err := s.store.Issue(s.scope, pool.Amount{Bytes: uint64(len(raw)), Messages: 1})
	if err != nil {
		result.Err = err
		return
	}
	defer func() { result.Err = errors.Join(result.Err, s.store.CloseGrant(grant.Ref)) }()
	a, err := s.store.Prepare(grant.Ref, s.folder, batch, uint64(len(raw)), body)
	if err != nil {
		result.Err = err
		return
	}
	result.Attempt = &a.Ref
	if err = ctx.Err(); err != nil {
		result.Err = errors.Join(err, s.store.CancelPrepared(a.Ref))
		return
	}
	if _, err = s.store.Start(a.Ref); err != nil {
		result.Err = err
		return
	}
	err = s.mail.Append(ctx, s.folder, raw)
	outcome := journal.Accepted
	if err != nil {
		outcome = journal.Uncertain
	}
	result.Err = errors.Join(err, s.store.Complete(a.Ref, outcome))
	return
}

// IsControlBody is an untrusted routing hint after MIME decoding. Only Open
// authorizes a frame; changing this marker cannot authenticate a record.
func IsControlBody(body []byte) bool { return len(body) >= 4 && bytes.Equal(body[:4], []byte("ITC1")) }

// Open authenticates exactly one bounded control message. The mailbox owner
// must demultiplex DATA before calling this and reserve room for the returned
// frame before consuming its replay counter. No automatic deletion or receipt
// is implied: the owner must apply the frame to its session state machine.
func Open(raw []byte, cipher *secure.Epoch) (wire.Frame, error) {
	if cipher == nil {
		return wire.Frame{}, ErrScope
	}
	// Maximum canonical base64 MIME size plus a bounded header allowance.
	if len(raw) > 8192+((MaxRecord+4+2)/3*4)*78/76+4 {
		return wire.Frame{}, wire.ErrLimit
	}
	packet, err := mailbox.Decode(raw)
	if err != nil {
		return wire.Frame{}, err
	}
	defer clear(packet)
	if !IsControlBody(packet) {
		return wire.Frame{}, ErrScope
	}
	if len(packet) > MaxRecord+4 {
		return wire.Frame{}, wire.ErrLimit
	}
	frame, err := cipher.OpenControl(packet[4:])
	if err != nil {
		return wire.Frame{}, err
	}
	if _, err = wire.DecodeControl(frame); err != nil {
		clear(frame.Payload)
		return wire.Frame{}, err
	}
	return frame, nil
}

// Reclaim releases physical capacity only after exact FETCH verification and
// confirmed deletion. Call only when a semantic response made this control
// obsolete, or its session is fenced. Success of Send alone is NOT sufficient.
// Message must come from this sender's route; recipients do not own its journal.
func (s *Sender) Reclaim(ctx context.Context, ref journal.AttemptRef, m mailbox.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref.Grant.Scope != s.scope || m.Folder != s.folder {
		return ErrScope
	}
	body, err := mailbox.Decode(m.MIME)
	if err != nil {
		return err
	}
	defer clear(body)
	loc := journal.Location{UIDValidity: m.Validity, UID: m.UID}
	if err = s.store.Observe(ref, s.store.Run(), journal.Observation{Account: s.scope.Account, Folder: s.folder, EncodedBytes: uint64(len(m.MIME)), Location: loc, Decoded: body}); err != nil {
		return err
	}
	if err = s.mail.Delete(ctx, m); err != nil {
		return err
	}
	return s.store.Deleted(ref, s.store.Run(), loc)
}

// RecoverDeletion covers a crash after EXPUNGE but before Deleted. A previously
// observed UID in the same UIDVALIDITY is mandatory. Missing unknown APPENDs
// without such a locator remain charged. This method does not delete messages.
func (s *Sender) RecoverDeletion(ctx context.Context, ref journal.AttemptRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ref.Grant.Scope != s.scope {
		return ErrScope
	}
	a, err := s.store.GetAttempt(ref)
	if err != nil {
		return err
	}
	if a.Folder != s.folder || a.Location.UID == 0 || a.State != journal.Committed {
		return ErrScope
	}
	absent, err := s.mail.Absent(ctx, s.folder, a.Location.UIDValidity, a.Location.UID)
	if err != nil {
		return err
	}
	if !absent {
		return journal.ErrState
	}
	return s.store.Deleted(ref, s.store.Run(), a.Location)
}
