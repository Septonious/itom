package mailbox

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"
)

// A provider may advertise BINARY but reject literal8 for an individual
// account. After an explicit UNKNOWN-CTE response, fence the uncertain APPEND
// and use ordinary MIME on a fresh socket. This bounded process-local cache
// does not affect other logins or persist a provider-wide blacklist.
var binaryRefusals = struct {
	sync.Mutex
	entries map[[32]byte]time.Time
}{entries: make(map[[32]byte]time.Time)}

const binaryRefusalTTL = 30 * time.Minute

func binaryPolicyKey(cfg Config) [32]byte {
	// Avoid retaining the username (or password) in the global map.
	h := sha256.New()
	h.Write([]byte(cfg.TLS))
	h.Write([]byte{0})
	h.Write([]byte(cfg.Host))
	h.Write([]byte{0})
	var port [4]byte
	binary.BigEndian.PutUint32(port[:], uint32(cfg.Port))
	h.Write(port[:])
	h.Write([]byte(cfg.Username))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key
}

func binaryAllowedByRefusals(key [32]byte) bool {
	binaryRefusals.Lock()
	defer binaryRefusals.Unlock()
	deadline, seen := binaryRefusals.entries[key]
	if !seen {
		return true
	}
	if time.Now().After(deadline) {
		delete(binaryRefusals.entries, key)
		return true
	}
	return false
}

func rememberBinaryRefusal(key [32]byte) {
	binaryRefusals.Lock()
	defer binaryRefusals.Unlock()
	if len(binaryRefusals.entries) >= 128 {
		// Keep memory bounded rather than accumulating old usernames forever.
		binaryRefusals.entries = make(map[[32]byte]time.Time)
	}
	binaryRefusals.entries[key] = time.Now().Add(binaryRefusalTTL)
}
