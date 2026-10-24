package transport_test

import (
	"errors"
	"testing"

	"itom.local/core/journal"
	"itom.local/core/mailbox"
	"itom.local/core/transport"
	"itom.local/core/wire"
)

func TestServerInboxCorrelatesOwnBodyWithoutPeerDecryptionOrQuotaRelease(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	p := serverBody(t, f, []byte("only for the client"))
	ref, _ := p.ServerAttempt()
	r, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	decode, err := transport.NewServerInboxDecoder(r)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Held("account1", "12345678")
	if err != nil {
		t.Fatal(err)
	}
	body, err := mailbox.Decode(p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	_, usage, packet, err := wire.OpenServerEnvelope(body, f.key, uint64(len(p.MIME())))
	if err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte(nil), packet...)
	mutated[len(mutated)-1] ^= 1
	_, forged, err := wire.BindServerEnvelope(usage.Key, wire.ServerData, usage.Attempt.Batch, "fast", mutated, f.key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if err != nil {
		t.Fatal(err)
	}
	forgedMIME, err := mailbox.Encode(forged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decode(9, transport.Body{UID: 13, Bytes: forgedMIME}); !errors.Is(err, journal.ErrScope) {
		t.Fatal("correlation substituted for full BODY ownership proof", err)
	}
	if _, err = decode(10, transport.Body{UID: 13, Bytes: p.MIME()}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("wrong mailbox incarnation accepted", err)
	}
	_, wrongLedger, err := wire.BindServerEnvelope(usage.Key, wire.ServerControl, usage.Attempt.Batch, "fast", packet, f.key, func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
	if err != nil {
		t.Fatal(err)
	}
	wrongMIME, err := mailbox.Encode(wrongLedger)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decode(9, transport.Body{UID: 13, Bytes: wrongMIME}); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("protocol origin borrowed DATA authority", err)
	}
	if delivery, err := decode(9, transport.Body{UID: 13, Bytes: p.MIME()}); err != nil || delivery != nil {
		t.Fatal("own BODY became peer application data", delivery, err)
	}
	a, err := f.store.GetAttempt(ref)
	if err != nil || a.State != journal.Committed || a.Location != (journal.Location{UIDValidity: 9, UID: 13}) {
		t.Fatal(a, err)
	}
	after, err := f.store.Held("account1", "12345678")
	if err != nil || after != before {
		t.Fatal("own proof released physical debt", before, after, err)
	}
	// The client can still authenticate/decrypt once after the own server scan.
	delivery, err := f.incoming.Decode(9, 13, p.MIME())
	if err != nil {
		t.Fatal("own scan consumed peer replay state", err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Audit(); err != nil {
		t.Fatal(err)
	}
}

func TestServerInboxRejectsDuplicateLedgerAndUnrelatedRoutes(t *testing.T) {
	f := serverPublications(t, transport.Fast, 4<<20)
	r, err := transport.NewRemoteReceiver(f.server, transport.DataLedger, f.serverKey, f.store, "account1", "12345678", "fast", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	other, err := transport.NewRemoteReceiver(f.server, transport.ControlLedger, f.serverKey, f.store, "account1", "12345678", "bulk", 9, f.key)
	if err != nil {
		t.Fatal(err)
	}
	for _, routes := range [][]*transport.RemoteReceiver{nil, {nil}, {r, r}, {r, other}} {
		if _, err := transport.NewServerInboxDecoder(routes...); !errors.Is(err, transport.ErrProtocol) {
			t.Fatal("ambiguous/mixed scope router accepted", err)
		}
	}
}
