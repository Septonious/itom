package mailbox

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"testing"
)

func TestMIMEOptimizedMatchesCanonicalWire(t *testing.T) {
	for _, n := range []int{1, 2, 3, 56, 57, 58, 114, 115, 32768, 336 << 10, MaxBody} {
		checkScenarioBoundary(t, fmt.Sprint(n), func(t *testing.T) {
			body := make([]byte, n)
			for i := range body {
				body[i] = byte(i * 29)
			}
			encoded := base64.StdEncoding.EncodeToString(body)
			want := []byte(mimeHeader)
			for len(encoded) > 0 {
				k := min(76, len(encoded))
				want = append(want, encoded[:k]...)
				want = append(want, '\r', '\n')
				encoded = encoded[k:]
			}
			got, err := Encode(body)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("wire changed", err)
			}
			decoded, err := Decode(got)
			if err != nil || !bytes.Equal(decoded, body) {
				t.Fatal("round trip", err)
			}
			damaged := bytes.Clone(got)
			damaged[len(mimeHeader)] = '!'
			if _, err = Decode(damaged); err == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
}
func BenchmarkBulkMIMEEncode(b *testing.B) {
	body := make([]byte, 336<<10)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Encode(body); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkBulkMIMEDecode(b *testing.B) {
	raw, _ := Encode(make([]byte, 336<<10))
	b.SetBytes(336 << 10)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Decode(raw); err != nil {
			b.Fatal(err)
		}
	}
}
