package transport

import (
	"errors"
	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"sync"
)

// RemoteReceiver is the server's CLIENT-origin path, not a shared-mailbox
// classifier. Local-origin bodies and other ledger classes must be routed to
// their own authority before this point; none may become peer application data.
type RemoteReceiver struct {
	session               *Session
	cipher                *Cipher
	store                 *journal.Store
	account, user, folder string
	validity              uint32
	class                 LedgerClass
	authenticationKey     [32]byte
}

func NewRemoteReceiver(s *Session, class LedgerClass, cipher *Cipher, store *journal.Store, account, user, folder string, validity uint32, key []byte) (*RemoteReceiver, error) {
	if class != DataLedger && class != ControlLedger || s == nil || cipher == nil || store == nil || account == "" || user == "" || folder == "" || validity == 0 || len(key) != 32 || cipher.epoch.Context().User != user {
		return nil, ErrProtocol
	}
	r := &RemoteReceiver{session: s, class: class, cipher: cipher, store: store, account: account, user: user, folder: folder, validity: validity}
	copy(r.authenticationKey[:], key)
	return r, nil
}

// StoredDelivery owns an authenticated record after durable physical accounting.
// If the logical receiver is temporarily full, retry this object, not AEAD.Open
// on the physical BODY: doing the latter would consume/reject its replay slot.
type StoredDelivery struct {
	mu        sync.Mutex
	session   *Session
	record    Record
	status    wire.CleanupStatus
	finished  bool
	discarded bool
}

func (r *RemoteReceiver) Decode(validity, uid uint32, raw []byte) (*StoredDelivery, error) {
	if validity != r.validity || uid == 0 {
		return nil, ErrProtocol
	}
	charge, err := mailbox.CanonicalEncodedBytes(raw)
	if err != nil {
		return nil, err
	}
	body, err := mailbox.Decode(raw)
	if err != nil {
		return nil, err
	}
	defer clear(body)
	usage, packet, err := wire.OpenDataEnvelope(body, r.authenticationKey[:], charge)
	if err != nil {
		return nil, err
	}
	binding := r.cipher.epoch.Context()
	if usage.Attempt == nil || usage.Key.Store != r.store.StoreID() || usage.Key.Account != r.account || usage.Key.User != r.user || usage.Key.Session != binding.Session || usage.Attempt.Folder != r.folder {
		return nil, ErrProtocol
	}
	// ApplyRemote verifies the actual issued grant and contiguous descriptor
	// prefix. A missing prefix returns ErrOrder before the replay counter moves.
	proof := journal.Observation{Account: r.account, Folder: r.folder, EncodedBytes: charge, Location: journal.Location{UIDValidity: validity, UID: uid}, Decoded: body}
	status, _, err := r.store.AcceptRemoteBody(r.user, r.store.Run(), usage, proof)
	if err != nil {
		return nil, err
	}
	record, err := r.cipher.Open(packet)
	if err != nil {
		return nil, err
	}
	if !r.class.accepts(record.Kind) {
		clear(record.Payload)
		return nil, ErrProtocol
	}
	return &StoredDelivery{session: r.session, record: record, status: status}, nil
}
func (d *StoredDelivery) Apply() error {
	if d == nil {
		return ErrProtocol
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.finished {
		return nil
	}
	err := d.session.Receive(d.record)
	if errors.Is(err, ErrClosed) {
		d.discarded = true
		d.finished = true
		clear(d.record.Payload)
		return nil
	}
	if err != nil {
		return err
	}
	d.finished = true
	clear(d.record.Payload)
	return nil
}
func (d *StoredDelivery) Status() wire.CleanupStatus { return d.status }
func (d *StoredDelivery) Discarded() bool            { d.mu.Lock(); defer d.mu.Unlock(); return d.discarded }
