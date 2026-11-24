package wire

import (
	"bytes"
	"crypto/sha256"
	"itom.local/core/mailbox"
	"testing"
)

// Authenticate fuzzed metadata so mutations exercise the bounded decoder, not
// just the outer MAC rejection. This never grants journal/wallet authority.
func FuzzAuthenticatedServerEnvelopeMetadataIsCanonicalAndBounded(f *testing.F) {
	key := bytes.Repeat([]byte{7}, 32)
	k := QuotaKey{Store: ID{1}, Epoch: 3, Grant: 4, Session: ID{2}, User: "12345678", Account: "account1"}
	size := func(body []byte) (uint64, error) { return mailbox.EncodedSize(len(body)) }
	_, seed, err := BindServerEnvelope(k, ServerData, ID{5}, "fast", []byte("packet"), key, size)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("ISC2"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) < 4+sha256.Size || len(input) > MaxDataEnvelopeBody {
			return
		}
		body := append([]byte(nil), input...)
		class := ServerData
		if body[2]&1 == 1 {
			class = ServerControl
		}
		copy(body[:4], class.magic())
		n := len(body) - sha256.Size
		copy(body[n:], serverEnvelopeMAC(class, key, body[:n]))
		charge, err := size(body)
		if err != nil {
			return
		}
		actualClass, usage, packet, err := OpenServerEnvelope(body, key, charge)
		if err != nil {
			return
		}
		_, canonical, err := BindServerEnvelope(usage.Key, actualClass, usage.Attempt.Batch, usage.Attempt.Folder, packet, key, size)
		if err != nil || !bytes.Equal(canonical, body) {
			t.Fatal("authenticated malformed/noncanonical metadata accepted", err)
		}
	})
}
