package wire

import "crypto/sha256"

// DataEnvelopeSize predicts the exact authenticated envelope length before
// encryption. Metadata fields have fixed-width counters, so no nonce, batch ID
// or durable spend is needed to choose the concrete grant's fitting quantum.
func DataEnvelopeSize(k QuotaKey, folder string, encryptedBytes int) (int, error) {
	if !validQuotaFolder(folder) || encryptedBytes <= 0 || encryptedBytes > MaxDataBatchBody {
		return 0, ErrLimit
	}
	key, err := encodeQuotaKey(k)
	if err != nil {
		return 0, err
	}
	size := len(DataEnvelopeMagic) + len(key) + 2 + len(folder) + 24 + 16 + 4 + encryptedBytes + sha256.Size
	if size > MaxDataEnvelopeBody {
		return 0, ErrLimit
	}
	return size, nil
}
