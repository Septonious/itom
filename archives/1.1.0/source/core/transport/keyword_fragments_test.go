package transport

import (
	"bytes"
	"strings"
	"testing"
)

func TestFragmentedReceiptsPreserveFullMACBodyAndPhysicalLocation(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	expected := receiptKeyword([]byte("fully authenticated original physical body"), 9, 17, key)
	for _, width := range []int{48, 20, 5} {
		flags := keywordFragments(expected[7:], 'r', width)
		for n := range flags {
			flags[n] = strings.ToUpper(flags[n])
		}
		if !verifiedReceiptKeyword(flags, expected) {
			t.Fatal("complete case-equivalent receipt rejected", width)
		}
		if verifiedReceiptKeyword(flags[1:], expected) {
			t.Fatal("partial receipt accepted", width)
		}
		for _, wrong := range []string{
			receiptKeyword([]byte("different body"), 9, 17, key),
			receiptKeyword([]byte("fully authenticated original physical body"), 9, 18, key),
			receiptKeyword([]byte("fully authenticated original physical body"), 10, 17, key),
			"ITOM2R_" + strings.Repeat("0", 64),
		} {
			// Construct a fully well-formed fragment hash around the wrong MAC.
			// Structural validity cannot replace authentication of all 256 bits.
			if verifiedReceiptKeyword(keywordFragments(wrong[7:], 'r', width), expected) {
				t.Fatal("forged/transplanted receipt accepted", width)
			}
		}
	}
	if !verifiedReceiptKeyword([]string{strings.ToLower(expected)}, expected) {
		t.Fatal("legal provider keyword casing rejected")
	}
}
