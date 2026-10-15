package mailbox

import (
	"bytes"
	"errors"
	"testing"
)

// A DATA verification view must be physically non-mutating, match the durable
// base64 charge and reject malformed/unbounded provider metadata.
func TestDecodeDataProofRetainsPhysicalRepresentation(t *testing.T) {
	body := bytes.Repeat([]byte{0, 1, 255, 13, 10, 4}, 30000)
	canonical, err := Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := binaryPhysical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := bytes.Replace(canonical, []byte("\r\n\r\n"), []byte("\r\nX-Provider: literal8 accepted\r\n\r\n"), 1)
	malformed := bytes.Replace(canonical, []byte("\r\n\r\n"), append([]byte("\r\nX-Padding: "), append(bytes.Repeat([]byte{'A'}, maxProviderMIMEHeaderDelta+1), []byte("\r\n\r\n")...)...), 1)
	for _, tc := range []struct {
		name     string
		raw      []byte
		accepted bool
	}{
		{"base64", canonical, true}, {"binary", binary, true}, {"provider_rewrite", rewritten, true}, {"oversized_rewrite", malformed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			unchanged := bytes.Clone(tc.raw)
			out, charged, e := DecodeDataProof(tc.raw)
			if !bytes.Equal(tc.raw, unchanged) {
				t.Fatal("physical evidence mutated")
			}
			if tc.accepted {
				if e != nil || charged != uint64(len(canonical)) || !bytes.Equal(out, body) {
					t.Fatalf("invalid proof charged=%d err=%v", charged, e)
				}
			} else if !errors.Is(e, ErrLimit) || out != nil || charged != 0 {
				t.Fatalf("unbounded proof accepted or leaked: %v", e)
			}
		})
	}
}

func BenchmarkDecodeDataProofBinary(b *testing.B) {
	body := bytes.Repeat([]byte{0, 127, 255, 10, 15, 3, 7, 12}, 32768)
	canonical, _ := Encode(body)
	physical, _ := binaryPhysical(canonical)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out, n, e := DecodeDataProof(physical)
		if e != nil || n != uint64(len(canonical)) || len(out) != len(body) {
			b.Fatal(e)
		}
	}
}

// Reader-level DATA must be able to reuse a single MIME decode without
// changing the canonical charge or widening provider-header acceptance.
func TestField68DataProofChargePredecoded(t *testing.T) {
	body := bytes.Repeat([]byte{0, 255, 13, 10, 7, 8, 9}, 13000)
	canonical, err := Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := binaryPhysical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := bytes.Replace(canonical, []byte("\r\n\r\n"), []byte("\r\nX-Provider: test\r\n\r\n"), 1)
	oversized := bytes.Replace(canonical, []byte("\r\n\r\n"), append([]byte("\r\nX-Padding: "), append(bytes.Repeat([]byte{'A'}, maxProviderMIMEHeaderDelta+1), []byte("\r\n\r\n")...)...), 1)
	for _, tc := range []struct {
		name     string
		physical []byte
		good     bool
	}{
		{"canonical", canonical, true}, {"binary", binary, true}, {"rewritten", rewritten, true}, {"oversized", oversized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rawBefore := bytes.Clone(tc.physical)
			decoded, err := Decode(tc.physical)
			if err != nil {
				t.Fatal(err)
			}
			n, err := DataProofCharge(tc.physical, decoded)
			if !bytes.Equal(tc.physical, rawBefore) {
				t.Fatal("physical bytes changed")
			}
			if tc.good {
				if err != nil || n != uint64(len(canonical)) || !bytes.Equal(decoded, body) {
					t.Fatalf("charge=%d err=%v", n, err)
				}
			} else if !errors.Is(err, ErrLimit) || n != 0 {
				t.Fatalf("oversized rewrite accepted n=%d err=%v", n, err)
			}
			clear(decoded)
		})
	}
}

func BenchmarkField68ReaderChargeReuse(b *testing.B) {
	body := bytes.Repeat([]byte{0, 127, 255, 10, 15, 3, 7, 12}, 32768)
	canonical, _ := Encode(body)
	binary, _ := binaryPhysical(canonical)
	for _, kind := range []struct {
		name string
		raw  []byte
	}{{"MIME", canonical}, {"BINARY", binary}} {
		b.Run(kind.name+"/double_decode", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				first, err := Decode(kind.raw)
				if err != nil {
					b.Fatal(err)
				}
				second, charge, err := DecodeDataProof(kind.raw)
				if err != nil || charge != uint64(len(canonical)) {
					b.Fatal(err)
				}
				clear(first)
				clear(second)
			}
		})
		b.Run(kind.name+"/single_decode", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for i := 0; i < b.N; i++ {
				first, err := Decode(kind.raw)
				if err != nil {
					b.Fatal(err)
				}
				charge, err := DataProofCharge(kind.raw, first)
				if err != nil || charge != uint64(len(canonical)) {
					b.Fatal(err)
				}
				clear(first)
			}
		})
	}
}
