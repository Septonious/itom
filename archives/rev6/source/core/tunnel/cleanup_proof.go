package tunnel

import (
	"crypto/sha256"
	"itom.local/core/mailbox"
)

// A sweep owns immutable Message.MIME buffers until its journal observations
// and deletions complete. Reuse parsing within that sweep, never across FETCH,
// reconnect, UIDVALIDITY changes, or cleanup invocations. This is only decoded
// content evidence: every locator and journal proof is still checked separately.
type cleanupProofKey struct {
	first *byte
	size  int
}
type cleanupBodyProof struct {
	body    []byte
	encoded uint64
	digest  [32]byte
	err     error
}
type cleanupProofs map[cleanupProofKey]cleanupBodyProof

func (p cleanupProofs) decode(raw []byte) cleanupBodyProof {
	key := cleanupProofKey{size: len(raw)}
	if len(raw) > 0 {
		key.first = &raw[0]
	}
	if proof, ok := p[key]; ok {
		return proof
	}
	body, encoded, err := mailbox.DecodeDataProof(raw)
	proof := cleanupBodyProof{body: body, encoded: encoded, err: err}
	if err == nil {
		proof.digest = sha256.Sum256(body)
	}
	p[key] = proof
	return proof
}

func (p cleanupProofs) clear() {
	for key, proof := range p {
		clear(proof.body)
		delete(p, key)
	}
}
