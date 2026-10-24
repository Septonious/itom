package wire_test

import (
	"bytes"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"testing"
)

func TestServerBodiesAuthenticateOriginLedgerAndExactPhysicalCharge(t *testing.T) {
	k := wire.QuotaKey{Store: wire.ID{1}, Epoch: 3, Grant: 9, Session: wire.ID{2}, User: "12345678", Account: "account1"}
	key := bytes.Repeat([]byte{4}, 32)
	size := func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) }
	for _, class := range []wire.ServerLedger{wire.ServerData, wire.ServerControl} {
		for _, n := range []int{1, 57, 65536, wire.MaxDataBatchBody} {
			payload := bytes.Repeat([]byte{7}, n)
			predicted, err := wire.ServerEnvelopeSize(k, class, "bulk", n)
			if err != nil {
				t.Fatal(err)
			}
			usage, body, err := wire.BindServerEnvelope(k, class, wire.ID{5}, "bulk", payload, key, size)
			if err != nil {
				t.Fatal(err)
			}
			mime, err := mailbox.Encode(body)
			if err != nil || len(body) != predicted || uint64(len(mime)) != usage.Attempt.EncodedBytes {
				t.Fatal("pre-admission geometry changed", err)
			}
			actualClass, actual, packet, err := wire.OpenServerEnvelope(body, key, uint64(len(mime)))
			if err != nil || actualClass != class || actual.Key != k || *actual.Attempt != *usage.Attempt || !bytes.Equal(payload, packet) {
				t.Fatal("server body changed in storage", err)
			}
			if _, _, err = wire.OpenDataEnvelope(body, key, uint64(len(mime))); err == nil {
				t.Fatal("server body accepted as a client grant report")
			}
			if _, _, _, err = wire.OpenServerEnvelope(body, key, uint64(len(mime))+1); err == nil {
				t.Fatal("noncanonical physical charge accepted")
			}
			flipped := append([]byte(nil), body...)
			if class == wire.ServerData {
				flipped[2] = 'C'
			} else {
				flipped[2] = 'D'
			}
			if _, _, _, err = wire.OpenServerEnvelope(flipped, key, uint64(len(mime))); err == nil {
				t.Fatal("changing the ledger retained valid authentication")
			}
		}
	}
	_, clientBody, err := wire.BindDataEnvelope(k, 1, wire.ID{6}, 0, "bulk", []byte("packet"), key, size)
	if err != nil {
		t.Fatal(err)
	}
	charge, err := size(clientBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = wire.OpenServerEnvelope(clientBody, key, charge); err == nil {
		t.Fatal("client grant report became server-origin DATA")
	}
	copy(clientBody[:4], "ISD2")
	if _, _, _, err = wire.OpenServerEnvelope(clientBody, key, charge); err == nil {
		t.Fatal("magic rewrite bypassed independent MAC domain")
	}
}
