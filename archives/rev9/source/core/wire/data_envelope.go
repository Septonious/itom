package wire

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// ErrDataEnvelopeDigest retains ErrFormat matching while identifying failed integrity.
var ErrDataEnvelopeDigest = errors.New("DATA_ENVELOPE_DIGEST")

const DataEnvelopeMagic = "ITE2"
const MaxDataEnvelopeBody = MaxDataBatchBody + 512

// BindDataEnvelope creates a self-describing physical attempt. Domain-separated
// HMAC authenticates its quota/account/folder/cumulative charge before any
// receiver may register it. DATA records still require Noise/AEAD validation.
// encodedSize must return the canonical carrier charge for a body of this length.
func BindDataEnvelope(k QuotaKey, ordinal uint64, batch ID, previousBytes uint64, folder string, encrypted, key []byte, encodedSize func([]byte) (uint64, error)) (UsageReport, []byte, error) {
	if len(key) != 32 || encodedSize == nil || !validQuotaFolder(folder) || ordinal == 0 || ordinal > 1<<24 || batch == (ID{}) || len(encrypted) == 0 || len(encrypted) > MaxDataBatchBody {
		return UsageReport{}, nil, ErrLimit
	}
	p, err := encodeQuotaKey(k)
	if err != nil {
		return UsageReport{}, nil, err
	}
	body := append([]byte(DataEnvelopeMagic), p...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(folder)))
	body = append(body, folder...)
	chargeAt := len(body)
	body = append(body, words(0, 0, ordinal)...)
	body = append(body, batch[:]...)
	body = binary.BigEndian.AppendUint32(body, uint32(len(encrypted)))
	body = append(body, encrypted...)
	body = append(body, make([]byte, sha256.Size)...)
	if len(body) > MaxDataEnvelopeBody {
		return UsageReport{}, nil, ErrLimit
	}
	charge, err := encodedSize(body)
	if err != nil {
		return UsageReport{}, nil, err
	}
	if charge == 0 || charge > 2<<20 || previousBytes > 1<<40-charge {
		return UsageReport{}, nil, ErrLimit
	}
	total := Quota{Bytes: previousBytes + charge, Messages: ordinal}
	if !total.valid() {
		return UsageReport{}, nil, ErrLimit
	}
	binary.BigEndian.PutUint64(body[chargeAt:], charge)
	binary.BigEndian.PutUint64(body[chargeAt+8:], total.Bytes)
	mac := dataEnvelopeMAC(key, body[:len(body)-sha256.Size])
	copy(body[len(body)-sha256.Size:], mac)
	report := UsageReport{Key: k, Total: total, Attempt: &PhysicalDescriptor{Batch: batch, EncodedBytes: charge, DecodedBytes: uint64(len(body)), Digest: sha256.Sum256(body), Folder: folder}}
	if _, err := EncodeControl(0, report); err != nil {
		return UsageReport{}, nil, err
	}
	return report, body, nil
}

func dataEnvelopeMAC(key, body []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("ITOM/0.9.2/physical-data-envelope/v2\x00"))
	h.Write(body)
	return h.Sum(nil)
}

// OpenDataEnvelope authenticates metadata and canonical charge. The returned
// payload is encrypted DATA, never authenticated application plaintext. The
// caller must check its expected user/account/session and authorized ledger.
func OpenDataEnvelope(body, key []byte, canonicalBytes uint64) (UsageReport, []byte, error) {
	if len(key) != 32 || len(body) > MaxDataEnvelopeBody || len(body) < 4+sha256.Size || !bytes.HasPrefix(body, []byte(DataEnvelopeMagic)) {
		return UsageReport{}, nil, ErrLimit
	}
	n := len(body) - sha256.Size
	if !hmac.Equal(dataEnvelopeMAC(key, body[:n]), body[n:]) {
		return UsageReport{}, nil, ErrFormat
	}
	r, payload, err := parseDataEnvelope(body)
	if err != nil || r.Attempt.EncodedBytes != canonicalBytes {
		return UsageReport{}, nil, ErrFormat
	}
	return r, payload, nil
}

// DataEnvelopePayload accepts only an independently authenticated immutable
// descriptor, as in legacy control reconciliation. It cannot establish trust
// from a clear header alone. Use OpenDataEnvelope on ordinary reception.
func DataEnvelopePayload(body []byte, expected UsageReport) ([]byte, error) {
	if expected.Attempt == nil || len(body) > MaxDataEnvelopeBody {
		return nil, ErrFormat
	}
	digest := sha256.Sum256(body)
	if digest != expected.Attempt.Digest {
		return nil, errors.Join(ErrFormat, ErrDataEnvelopeDigest)
	}
	r, payload, err := parseDataEnvelopeWithDigest(body, digest)
	if err != nil || r.Key != expected.Key || r.Total != expected.Total || *r.Attempt != *expected.Attempt {
		return nil, ErrFormat
	}
	return payload, nil
}

func parseDataEnvelope(body []byte) (UsageReport, []byte, error) {
	if len(body) > MaxDataEnvelopeBody {
		return UsageReport{}, nil, ErrLimit
	}
	return parseDataEnvelopeWithDigest(body, sha256.Sum256(body))
}

// digest belongs to these exact immutable bytes. This private helper is called
// only after the enclosing entry point computes it; it is never supplied by a peer.
func parseDataEnvelopeWithDigest(body []byte, digest [32]byte) (UsageReport, []byte, error) {
	if len(body) > MaxDataEnvelopeBody || len(body) < 4+sha256.Size || !bytes.HasPrefix(body, []byte(DataEnvelopeMagic)) {
		return UsageReport{}, nil, ErrLimit
	}
	k, p, err := decodeQuotaKey(body[4:])
	if err != nil || len(p) < 2 {
		return UsageReport{}, nil, ErrFormat
	}
	folderBytes := int(binary.BigEndian.Uint16(p))
	p = p[2:]
	if folderBytes > 255 || len(p) < folderBytes+44+sha256.Size {
		return UsageReport{}, nil, ErrLimit
	}
	folder := string(p[:folderBytes])
	p = p[folderBytes:]
	charge, totalBytes, ordinal := binary.BigEndian.Uint64(p), binary.BigEndian.Uint64(p[8:]), binary.BigEndian.Uint64(p[16:])
	var batch ID
	copy(batch[:], p[24:40])
	payloadBytes := uint64(binary.BigEndian.Uint32(p[40:]))
	p = p[44:]
	if payloadBytes == 0 || payloadBytes > MaxDataBatchBody || payloadBytes != uint64(len(p)-sha256.Size) || charge == 0 || totalBytes < charge {
		return UsageReport{}, nil, ErrLimit
	}
	r := UsageReport{Key: k, Total: Quota{Bytes: totalBytes, Messages: ordinal}, Attempt: &PhysicalDescriptor{Batch: batch, EncodedBytes: charge, DecodedBytes: uint64(len(body)), Digest: digest, Folder: folder}}
	if _, err := EncodeControl(0, r); err != nil {
		return UsageReport{}, nil, err
	}
	return r, p[:payloadBytes], nil
}
