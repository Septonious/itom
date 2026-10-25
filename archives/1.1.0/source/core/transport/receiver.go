package transport

import (
	"errors"
	"itom.local/core/journal"
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
	if class != DataLedger && class != ControlLedger || s == nil || cipher == nil || cipher.epoch == nil || store == nil || account == "" || user == "" || folder == "" || validity == 0 || len(key) != 32 || cipher.epoch.Context().User != user {
		return nil, ErrProtocol
	}
	if err := s.bindCipher(cipher, wire.S2C); err != nil {
		return nil, err
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
	records   []Record
	next      int
	status    wire.CleanupStatus
	finished  bool
	discarded bool
	reclaim   *reclaimBody
}

func (r *RemoteReceiver) Decode(validity, uid uint32, raw []byte) (*StoredDelivery, error) {
	if validity != r.validity || uid == 0 {
		return nil, ErrProtocol
	}
	envelope, err := openMailboxEnvelope(raw, r.authenticationKey[:])
	if err != nil {
		return nil, err
	}
	defer clear(envelope.body)
	return r.receiveEnvelope(validity, uid, envelope)
}

func (r *RemoteReceiver) receiveEnvelope(validity, uid uint32, envelope mailboxEnvelope) (*StoredDelivery, error) {
	return r.receiveAdmittedEnvelope(validity, uid, envelope, nil)
}

func (r *RemoteReceiver) receiveAdmittedEnvelope(validity, uid uint32, envelope mailboxEnvelope, admit func([]Record) error) (*StoredDelivery, error) {
	if validity != r.validity || uid == 0 || envelope.class != 0 {
		return nil, ErrProtocol
	}
	usage := envelope.usage
	binding := r.cipher.epoch.Context()
	if usage.Attempt == nil || usage.Key.Store != r.store.StoreID() || usage.Key.Account != r.account || usage.Key.User != r.user || usage.Key.Session != binding.Session || usage.Attempt.Folder != r.folder {
		return nil, ErrProtocol
	}
	// ApplyRemote verifies the actual issued grant and contiguous descriptor
	// prefix. A missing prefix returns ErrOrder before the replay counter moves.
	proof := journal.Observation{Account: r.account, Folder: r.folder, EncodedBytes: envelope.charge, Location: journal.Location{UIDValidity: validity, UID: uid}, Decoded: envelope.body}
	status, ref, err := r.store.AcceptRemoteBody(r.user, r.store.Run(), usage, proof)
	if err != nil {
		return nil, err
	}
	records, err := r.cipher.openRecordsAdmitted(envelope.packet, r.class, r.session.generation, r.session.authorityAdmission(r.class, admit))
	if err != nil {
		return nil, err
	}
	return &StoredDelivery{session: r.session, records: records, status: status, reclaim: &reclaimBody{store: r.store, ref: ref, location: proof.Location, account: r.account, folder: r.folder}}, nil
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
	for d.next < len(d.records) {
		record := &d.records[d.next]
		if record.Kind == Authority {
			d.session.mu.Lock()
			owner := d.session.authority
			closed := d.session.closed
			generation := d.session.generation
			d.session.mu.Unlock()
			if closed {
				return ErrClosed
			}
			if owner == nil || record.Generation != generation {
				return ErrProtocol
			}
			if err := owner.apply(record.Payload); err != nil {
				return err
			}
			clear(record.Payload)
			d.next++
			continue
		}
		// Coalesce ordinary feedback under one actor turn, but never hold this
		// lock while an authority operation commits a wallet/journal change.
		d.session.mu.Lock()
		for d.next < len(d.records) && d.records[d.next].Kind != Authority {
			record = &d.records[d.next]
			err := d.session.receiveLocked(*record)
			if errors.Is(err, ErrClosed) {
				d.discarded = true
			} else if err != nil {
				d.session.mu.Unlock()
				return err
			}
			clear(record.Payload)
			d.next++
		}
		d.session.mu.Unlock()
	}
	if d.status.Key.Store != (wire.ID{}) {
		d.session.mu.Lock()
		owner := d.session.authority
		d.session.mu.Unlock()
		if owner != nil {
			if err := owner.observeStoredStatus(d.status); err != nil {
				return err
			}
		}
	}
	d.finished = true
	return nil
}
func (d *StoredDelivery) Status() wire.CleanupStatus { return d.status }
func (d *StoredDelivery) Discarded() bool            { d.mu.Lock(); defer d.mu.Unlock(); return d.discarded }
