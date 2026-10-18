package datapath

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"itom.local/core/mailbox"
	"itom.local/core/secure"
	"itom.local/core/wire"
	"testing"
)

// A fresh Noise epoch is required per fixture. Reusing a ciphertext against a
// newly handshaken receiver would test a DIFFERENT key, not DATA validation.
func field68BatchFixture(t *testing.T) ([]byte, []byte, []byte, []byte, wire.UsageReport, *secure.Epoch) {
	t.Helper()
	sid := wire.ID{0x53}
	key := wire.QuotaKey{Store: wire.ID{8}, Epoch: 1, Grant: 1, Session: sid, User: "12345678", Account: "a01"}
	batch := wire.ID{9}
	tx, rx := epochs(t, sid)
	var packets [][]byte
	for i := 0; i < 3; i++ {
		packet, e := tx.Seal(wire.Frame{Type: wire.Data, Stream: uint64(2*i + 1), Logical: 1, Payload: bytes.Repeat([]byte{byte(i + 1)}, 8192)})
		if e != nil {
			t.Fatal(e)
		}
		packets = append(packets, packet)
	}
	packed, e := wire.PackDataBatch(batch, packets)
	if e != nil {
		t.Fatal(e)
	}
	bound, e := wire.BindPhysical(key, 1, batch, packed)
	if e != nil {
		t.Fatal(e)
	}
	canonical, e := mailbox.Encode(bound)
	if e != nil {
		t.Fatal(e)
	}
	report := wire.UsageReport{Key: key, Total: wire.Quota{Bytes: uint64(len(canonical)), Messages: 1}, Attempt: &wire.PhysicalDescriptor{Batch: batch, EncodedBytes: uint64(len(canonical)), DecodedBytes: uint64(len(bound)), Digest: sha256.Sum256(bound), Folder: "u-up"}}
	prefix := bytes.Index(canonical, []byte("\r\n\r\n")) + 4
	if prefix < 4 {
		t.Fatal("no MIME header")
	}
	binaryHeader := bytes.Replace(bytes.Clone(canonical[:prefix]), []byte("Content-Transfer-Encoding: base64"), []byte("Content-Transfer-Encoding: binary"), 1)
	binary := append(binaryHeader, bound...)
	rewritten := bytes.Replace(canonical, []byte("\r\n\r\n"), []byte("\r\nX-Provider: provider-header-rewrite\r\n\r\n"), 1)
	return canonical, binary, rewritten, bound, report, rx
}

func TestField68BatchBodyMatchesCanonicalVerification(t *testing.T) {
	for _, name := range []string{"canonical", "binary", "rewritten"} {
		t.Run(name, func(t *testing.T) {
			canonical, binary, rewritten, bound, report, rx := field68BatchFixture(t)
			physical := canonical
			switch name {
			case "binary":
				physical = binary
			case "rewritten":
				physical = rewritten
			}
			untouched := bytes.Clone(physical)
			decoded, charged, e := mailbox.DecodeDataProof(physical)
			if e != nil || charged != uint64(len(canonical)) || !bytes.Equal(decoded, bound) {
				t.Fatalf("decode size=%d err=%v", charged, e)
			}
			frames, e := OpenRemoteBatchBody(decoded, charged, report, rx)
			if e != nil || len(frames) != 3 {
				t.Fatalf("authenticated frames=%d err=%v", len(frames), e)
			}
			for i, f := range frames {
				if f.Stream != uint64(2*i+1) || !bytes.Equal(f.Payload, bytes.Repeat([]byte{byte(i + 1)}, 8192)) {
					t.Fatalf("record %d corrupt", i)
				}
				clear(f.Payload)
			}
			clear(decoded)
			if !bytes.Equal(physical, untouched) {
				t.Fatal("physical MIME changed; journal proof corrupt")
			}
		})
	}
	t.Run("legacy canonical path remains valid", func(t *testing.T) {
		canonical, _, _, _, report, rx := field68BatchFixture(t)
		frames, e := OpenRemoteBatch(canonical, report, rx)
		if e != nil || len(frames) != 3 {
			t.Fatalf("legacy path rejected %v", e)
		}
		for _, f := range frames {
			clear(f.Payload)
		}
	})
	t.Run("altered DATA rejected by digest", func(t *testing.T) {
		_, binary, _, _, report, rx := field68BatchFixture(t)
		decoded, charged, e := mailbox.DecodeDataProof(binary)
		if e != nil {
			t.Fatal(e)
		}
		decoded[len(decoded)-1] ^= 1
		_, e = OpenRemoteBatchBody(decoded, charged, report, rx)
		clear(decoded)
		if !errors.Is(e, ErrState) || ValidationStage(e) != "digest" {
			t.Fatalf("tamper not rejected: %v", e)
		}
	})
	t.Run("incorrect encoded size rejected", func(t *testing.T) {
		_, _, rewritten, _, report, rx := field68BatchFixture(t)
		decoded, charged, e := mailbox.DecodeDataProof(rewritten)
		if e != nil {
			t.Fatal(e)
		}
		_, e = OpenRemoteBatchBody(decoded, charged-1, report, rx)
		clear(decoded)
		if !errors.Is(e, ErrState) || ValidationStage(e) != "encoded_size" {
			t.Fatalf("size not rejected: %v", e)
		}
	})
	t.Run("wrong grant rejected", func(t *testing.T) {
		_, binary, _, _, report, rx := field68BatchFixture(t)
		decoded, charged, e := mailbox.DecodeDataProof(binary)
		if e != nil {
			t.Fatal(e)
		}
		report.Key.Grant++
		_, e = OpenRemoteBatchBody(decoded, charged, report, rx)
		clear(decoded)
		if !errors.Is(e, ErrState) || ValidationStage(e) != "physical_binding" {
			t.Fatalf("grant not rejected: %v", e)
		}
	})
}

// A provider may append CRLFs to the canonical base64 MIME after accepting
// APPEND. The encrypted ITQ1/ITD3 bytes are intact, but the physical literal
// length differs from the canonical grant charge. The receiver must verify
// the unchanged ciphertext and accept the original canonical charge, without
// using a reconstructed MIME body as deletion proof.
func TestField77ProviderAppendedCRLFDoesNotRejectAuthenticatedData(t *testing.T) {
	canonical, _, _, bound, report, rx := field68BatchFixture(t)
	rewritten := append(bytes.Clone(canonical), []byte("\r\n\r\n")...)
	immutable := bytes.Clone(rewritten)
	body, charged, err := mailbox.DecodeDataProof(rewritten)
	if err != nil || charged != report.Attempt.EncodedBytes || !bytes.Equal(body, bound) {
		t.Fatalf("bad canonical charge %d want %d err=%v", charged, report.Attempt.EncodedBytes, err)
	}
	frames, err := OpenRemoteBatchBody(body, charged, report, rx)
	if err != nil || len(frames) != 3 {
		t.Fatalf("authenticated DATA refused: count=%d err=%v", len(frames), err)
	}
	for i, f := range frames {
		if f.Stream != uint64(i*2+1) || len(f.Payload) != 8192 || !bytes.Equal(f.Payload, bytes.Repeat([]byte{byte(i + 1)}, 8192)) {
			t.Fatalf("invalid TCP stream frame %d", i)
		}
		clear(f.Payload)
	}
	clear(body)
	if !bytes.Equal(rewritten, immutable) {
		t.Fatal("physical UID/deletion proof mutated")
	}
}

func TestField77RewriteDoesNotBypassAuthenticatedDigest(t *testing.T) {
	canonical, _, _, _, report, rx := field68BatchFixture(t)
	rewritten := append(bytes.Clone(canonical), []byte("\r\n")...)
	body, charged, err := mailbox.DecodeDataProof(rewritten)
	if err != nil {
		t.Fatal(err)
	}
	body[10] ^= 1
	_, err = OpenRemoteBatchBody(body, charged, report, rx)
	clear(body)
	if !errors.Is(err, ErrState) || ValidationStage(err) != "digest" {
		t.Fatalf("tampered DATA accepted after normalization: %v", err)
	}
}
