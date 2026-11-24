package mailstream

import (
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

// Compact URL-safe tokens retain 128 bits of namespace/session discrimination.
// The profile/protocol identity is unchanged; only its mailbox representation
// changes. Fixed-length parsing does not confuse '-' inside base64url tokens.
func (l *IMAPLink) folderPrefix() string {
	sum := sha256.Sum256([]byte("ITOM mailbox namespace\x00" + l.identity))
	return "itom-" + base64.RawURLEncoding.EncodeToString(sum[:16])
}
func (l *IMAPLink) controlFolder(out bool) string {
	direction := "d"
	if l.client == out {
		direction = "u"
	}
	return l.folderPrefix() + "-c-" + direction
}
func (l *IMAPLink) dataFolder(id SessionID, out bool) string {
	direction := "d"
	if l.client == out {
		direction = "u"
	}
	return l.folderPrefix() + "-" + base64.RawURLEncoding.EncodeToString(id[:]) + "-" + direction
}

// Only recognized inactive DATA in this namespace qualifies. The current
// session and its two control folders are never historical collection targets.
func (l *IMAPLink) obsoleteFolder(name string, current SessionID) bool {
	if strings.HasPrefix(name, l.folderPrefix()+"-") {
		rest := strings.TrimPrefix(name, l.folderPrefix()+"-")
		if len(rest) < 24 {
			return false
		}
		raw, e := base64.RawURLEncoding.Strict().DecodeString(rest[:22])
		if e != nil || len(raw) != 16 {
			return false
		}
		var id SessionID
		copy(id[:], raw)
		if id == (SessionID{}) || id == current {
			return false
		}
		tail := rest[22:]
		if tail == "-u" || tail == "-d" || tail == "-u-p" || tail == "-d-p" {
			return true
		}
		if len(tail) != 5 || tail[0] != '-' || tail[1] != 'u' && tail[1] != 'd' || tail[2] != '-' {
			return false
		}
		slot, e := strconv.ParseUint(tail[3:], 16, 8)
		return e == nil && slot < uint64(dataSlots)
	}
	return false
}
