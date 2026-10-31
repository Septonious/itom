package transport

import (
	"itom.local/core/mailbox"
	"itom.local/core/wire"
)

// A scoped inbox opens MIME and authenticates the physical envelope once.
// Record AEAD remains a separate peer-only step after authority validation.
// packet aliases body; the caller clears body after synchronous processing.
type mailboxEnvelope struct {
	body, packet []byte
	charge       uint64
	class        wire.ServerLedger // zero is CLIENT origin, never server DATA/protocol
	usage        wire.UsageReport
}

func openMailboxEnvelope(raw, key []byte) (mailboxEnvelope, error) {
	var e mailboxEnvelope
	charge, err := mailbox.CanonicalEncodedBytes(raw)
	if err != nil {
		return e, err
	}
	body, err := mailbox.Decode(raw)
	if err != nil {
		return e, err
	}
	e.body, e.charge = body, charge
	if len(body) >= 4 && (string(body[:4]) == "ISD2" || string(body[:4]) == "ISC2") {
		e.class, e.usage, e.packet, err = wire.OpenServerEnvelope(body, key, charge)
	} else {
		e.usage, e.packet, err = wire.OpenDataEnvelope(body, key, charge)
	}
	if err != nil {
		clear(body)
		return mailboxEnvelope{}, err
	}
	return e, nil
}
