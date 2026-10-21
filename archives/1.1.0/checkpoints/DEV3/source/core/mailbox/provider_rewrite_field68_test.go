package mailbox

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestProviderRewriteCanonicalizationAndBounds(t *testing.T) {
	body := bytes.Repeat([]byte{0, 0xff, 0x35, 0x50, 0x13, 0x0a}, 1400)
	canonical, err := Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		raw  []byte
		ok   bool
	}{
		{"unaltered", canonical, true},
		{"extra_header", bytes.Replace(canonical, []byte("\r\n\r\n"), []byte("\r\nX-Provider-Header: received\r\n\r\n"), 1), true},
		{"overgrown_header", bytes.Replace(canonical, []byte("\r\n\r\n"), []byte("\r\nX-Padding: "+string(bytes.Repeat([]byte{'X'}, maxProviderMIMEHeaderDelta+1))+"\r\n\r\n"), 1), false},
		{"wrong_encoding", bytes.Replace(canonical, []byte("Content-Transfer-Encoding: base64"), []byte("Content-Transfer-Encoding: quoted-printable"), 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			size, err := CanonicalEncodedBytes(tc.raw)
			converted, err2 := CanonicalDataMIME(tc.raw)
			if tc.ok {
				if err != nil || err2 != nil || size != uint64(len(canonical)) || !bytes.Equal(converted, canonical) {
					t.Fatalf("canonical proof failed: size=%d errs=%v %v", size, err, err2)
				}
			} else if err == nil || err2 == nil {
				t.Fatalf("untrusted MIME accepted: %v %v", err, err2)
			}
		})
	}
}

// Rebuilding MIME is never permission to alter the authenticated body.
// The downstream AEAD/digest validation must reject a different body, even
// when its length and reconstructed canonical MIME size are identical.
func TestProviderRewritePreservesAuthenticatedBody(t *testing.T) {
	original := []byte("original-body-content")
	corrupted := []byte("tampered-body-content")
	if len(original) != len(corrupted) {
		t.Fatal("bad test fixture")
	}
	raw, _ := Encode(corrupted)
	rewritten := bytes.Replace(raw, []byte("\r\n\r\n"), []byte("\r\nX-Received: copied\r\n\r\n"), 1)
	normalized, err := CanonicalDataMIME(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(decoded) == sha256.Sum256(original) || bytes.Equal(decoded, original) {
		t.Fatal("rewritten physical data was substituted for authenticated content")
	}
}

func BenchmarkProviderCanonicalizeBinary(b *testing.B) {
	body := bytes.Repeat([]byte{0, 127, 255, 10, 15, 3, 7, 12}, 32768)
	canonical, _ := Encode(body)
	physical, err := binaryPhysical(canonical)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, e := CanonicalDataMIME(physical)
		if e != nil || len(out) != len(canonical) {
			b.Fatal(e)
		}
	}
}
