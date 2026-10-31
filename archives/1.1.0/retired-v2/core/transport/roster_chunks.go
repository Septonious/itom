package transport

import (
	"crypto/sha256"
	"encoding/binary"
)

// A per-account manifest may exceed one encrypted record. Canonical fixed-size
// chunks retain the original 32KiB roster publication bound and sixteen known
// startup UID bound: at most four offers plus ACCEPT/READY and eight bootstrap
// bodies. Parts may arrive out of order; no incomplete offer opens a wallet.
func rosterChunk(encoded []byte, offset int, digest [32]byte) []byte {
	n := min(maxRosterChunk, len(encoded)-offset)
	raw := make([]byte, rosterChunkHeader+n)
	binary.BigEndian.PutUint32(raw[:4], uint32(offset))
	binary.BigEndian.PutUint32(raw[4:8], uint32(len(encoded)))
	copy(raw[8:40], digest[:])
	copy(raw[40:], encoded[offset:offset+n])
	return raw
}
func (d *RosterDriver) validateOfferChunk(raw []byte) (candidate []byte, manifest RosterManifest, complete bool, err error) {
	if len(raw) < rosterChunkHeader || len(raw) > maxRosterBytes {
		return nil, manifest, false, ErrProtocol
	}
	offset, total := int(binary.BigEndian.Uint32(raw[:4])), int(binary.BigEndian.Uint32(raw[4:8]))
	expected := rosterHeader + rosterEntry*len(d.plan.Accounts)
	if total != expected || total > maxRosterManifestBytes || offset < 0 || offset >= total || offset%maxRosterChunk != 0 || len(raw)-rosterChunkHeader != min(maxRosterChunk, total-offset) {
		return nil, manifest, false, ErrProtocol
	}
	var digest [32]byte
	copy(digest[:], raw[8:40])
	if d.offerTotal > 0 && (total != d.offerTotal || digest != d.offerDigest) {
		return nil, manifest, false, ErrProtocol
	}
	if _, duplicate := d.offerParts[offset]; duplicate {
		return nil, manifest, false, ErrProtocol
	}
	chunks := (total + maxRosterChunk - 1) / maxRosterChunk
	if len(d.offerParts)+1 < chunks {
		return nil, manifest, false, nil
	}
	if len(d.offerParts)+1 != chunks {
		return nil, manifest, false, ErrProtocol
	}
	candidate = make([]byte, 0, total)
	for at := 0; at < total; at += maxRosterChunk {
		part := d.offerParts[at]
		if at == offset {
			part = raw[40:]
		}
		if len(part) != min(maxRosterChunk, total-at) {
			return nil, manifest, false, ErrProtocol
		}
		candidate = append(candidate, part...)
	}
	if sha256.Sum256(candidate) != digest {
		return nil, manifest, false, ErrProtocol
	}
	manifest, err = decodeRoster(candidate, d.plan, d.planHash)
	if err != nil {
		return nil, manifest, false, err
	}
	if !validRosterAnchor(manifest, d.input.origin) {
		return nil, manifest, false, ErrProtocol
	}
	for n, a := range manifest.Accounts {
		if a.FastUIDValidity != d.lanes[2*n].Executor.validity || a.BulkUIDValidity != d.lanes[2*n+1].Executor.validity {
			return nil, manifest, false, ErrProtocol
		}
	}
	return candidate, manifest, true, nil
}
