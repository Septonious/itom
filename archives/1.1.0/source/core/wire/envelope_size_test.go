package wire_test

import (
	"bytes"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"testing"
)

func TestAdmissionSizeMatchesActualAuthenticatedEnvelopeAndCanonicalMIME(t *testing.T) {
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 9, Grant: 3, Session: wire.ID{2}, User: "12345678", Account: "account_123"}
	for _, n := range []int{1, 16, 33, 34, 49, 50, 56, 57, 58, 76, 114, 1024, 65536} {
		for _, folder := range []string{"fast", "different scoped bulk folder"} {
			predicted, e := wire.DataEnvelopeSize(key, folder, n)
			if e != nil {
				t.Fatal(e)
			}
			report, body, e := wire.BindDataEnvelope(key, 1, wire.ID{3}, 0, folder, make([]byte, n), bytes.Repeat([]byte{4}, 32), func(b []byte) (uint64, error) { return mailbox.EncodedSize(len(b)) })
			if e != nil {
				t.Fatal(e)
			}
			mime, e := mailbox.Encode(body)
			if e != nil {
				t.Fatal(e)
			}
			if len(body) != predicted || uint64(len(mime)) != report.Attempt.EncodedBytes {
				t.Fatal("sizing required nonce/assembly retry", n, predicted, len(body), len(mime))
			}
		}
	}
}
