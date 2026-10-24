package transport

import (
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// ServerOrigin must come from authenticated session/profile negotiation, not
// an untrusted mailbox header. It pins one ledger store, run and incarnation.
type ServerOrigin struct {
	Store                 wire.ID
	Run                   uint64
	Account, User, Folder string
	UIDValidity           uint32
}

// ClientReceiver receives SERVER-origin bodies only. The issuer already holds
// their durable local attempts; receiving DATA cannot spend a client wallet,
// reconcile old remote grants or authorize any refund.
type ClientReceiver struct {
	session           *Session
	cipher            *Cipher
	origin            ServerOrigin
	class             LedgerClass
	authenticationKey [32]byte
}

func NewClientReceiver(s *Session, class LedgerClass, cipher *Cipher, origin ServerOrigin, key []byte) (*ClientReceiver, error) {
	if s == nil || cipher == nil || class != DataLedger && class != ControlLedger || len(key) != 32 || origin.UIDValidity == 0 || origin.User != cipher.epoch.Context().User {
		return nil, ErrProtocol
	}
	shape := wire.QuotaKey{Store: origin.Store, Epoch: origin.Run, Grant: 1, Session: cipher.epoch.Context().Session, Account: origin.Account, User: origin.User}
	if _, err := wire.ServerEnvelopeSize(shape, wire.ServerData, origin.Folder, 1); err != nil {
		return nil, ErrProtocol
	}
	r := &ClientReceiver{session: s, cipher: cipher, origin: origin, class: class}
	copy(r.authenticationKey[:], key)
	return r, nil
}

// ClientDelivery intentionally has no CleanupStatus method. A peer's observed
// physical descriptor is useful for correlation, never issuer refund authority.
type ClientDelivery struct {
	delivery   *StoredDelivery
	descriptor wire.UsageReport
}

func (r *ClientReceiver) Decode(validity, uid uint32, raw []byte) (*ClientDelivery, error) {
	if validity != r.origin.UIDValidity || uid == 0 {
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
	class, usage, packet, err := wire.OpenServerEnvelope(body, r.authenticationKey[:], charge)
	if err != nil {
		return nil, err
	}
	expectedClass := wire.ServerData
	if r.class == ControlLedger {
		expectedClass = wire.ServerControl
	}
	if class != expectedClass || usage.Key.Store != r.origin.Store || usage.Key.Epoch != r.origin.Run || usage.Key.Account != r.origin.Account || usage.Key.User != r.origin.User || usage.Key.Session != r.cipher.epoch.Context().Session || usage.Attempt.Folder != r.origin.Folder {
		return nil, ErrProtocol
	}
	records, err := r.cipher.openRecordsFor(packet, r.class, r.session.generation)
	if err != nil {
		return nil, err
	}
	return &ClientDelivery{delivery: &StoredDelivery{session: r.session, records: records}, descriptor: usage}, nil
}

func (d *ClientDelivery) Apply() error {
	if d == nil || d.delivery == nil {
		return ErrProtocol
	}
	return d.delivery.Apply()
}
func (d *ClientDelivery) Discarded() bool { return d.delivery.Discarded() }
func (d *ClientDelivery) Descriptor() wire.UsageReport {
	usage := d.descriptor
	if usage.Attempt != nil {
		copy := *usage.Attempt
		usage.Attempt = &copy
	}
	return usage
}
