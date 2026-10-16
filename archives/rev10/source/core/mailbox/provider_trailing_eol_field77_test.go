package mailbox

import (
	"bytes"
	"errors"
	"testing"
)

// The real IMAP capture had intact authenticated ITQ1 bytes on both ends but
// every server-side DATA proof failed the encoded-size check. A provider may
// preserve a canonical MIME header and add harmless line terminators. Such a
// representation must not change the canonical durable charge; the physical
// bytes still remain immutable evidence for UID and deletion validation.
func TestField77PhysicalLineTerminatorKeepsCanonicalCharge(t *testing.T) {
	body := bytes.Repeat([]byte{0x00, 0x0a, 0x0d, 0xff, 0x34}, 200)
	canonical, err := Encode(body)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{2, 4, 12} {
		t.Run(string(rune('A'+n)), func(t *testing.T) {
			physical := append(bytes.Clone(canonical), bytes.Repeat([]byte("\r\n"), n/2)...)
			saved := bytes.Clone(physical)
			decoded, err := Decode(physical)
			if err != nil || !bytes.Equal(decoded, body) {
				t.Fatalf("decode: %v", err)
			}
			predecodedCharge, err := DataProofCharge(physical, decoded)
			if err != nil || predecodedCharge != uint64(len(canonical)) {
				t.Fatalf("predecoded charge=%d want=%d err=%v", predecodedCharge, len(canonical), err)
			}
			decoded2, charge, err := DecodeDataProof(physical)
			if err != nil || charge != uint64(len(canonical)) || !bytes.Equal(decoded2, body) {
				t.Fatalf("full decode charge=%d err=%v", charge, err)
			}
			charged, err := CanonicalEncodedBytes(physical)
			if err != nil || charged != uint64(len(canonical)) {
				t.Fatalf("recovery charge=%d err=%v", charged, err)
			}
			if !bytes.Equal(physical, saved) {
				t.Fatal("physical proof mutated")
			}
		})
	}
	huge := append(bytes.Clone(canonical), bytes.Repeat([]byte("\r\n"), 257)...)
	decoded, err := Decode(huge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DataProofCharge(huge, decoded); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized physical rewrite accepted: %v", err)
	}
	if _, err := CanonicalEncodedBytes(huge); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized recovery proof accepted: %v", err)
	}
}
