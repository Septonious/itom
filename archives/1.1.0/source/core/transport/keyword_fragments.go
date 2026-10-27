package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// Encoded fragments are bounded routing/receipt hints only. Callers must still
// authenticate the complete original MAC before granting or releasing anything.
func keywordFragments(payload string, marker byte, width int) []string {
	if (len(payload) != 64 && len(payload) != 96) || width != 48 && width != 20 && width != 5 {
		return nil
	}
	payload = strings.ToLower(payload)
	raw, err := hex.DecodeString(payload)
	if err != nil {
		return nil
	}
	digest := sha256.Sum256(raw)
	group := hex.EncodeToString(digest[:4])
	if padding := (width - len(payload)%width) % width; padding != 0 {
		payload += strings.Repeat("0", padding)
	}
	var flags []string
	for offset := 0; offset < len(payload); offset += width {
		flags = append(flags, string(marker)+group+string(rune('a'+offset/width))+"_"+payload[offset:offset+width])
	}
	return flags
}

func decodeKeywordFragments(flags []string, marker byte, nibbles int) map[string][]string {
	if len(flags) > 64 || nibbles != 64 && nibbles != 96 {
		return nil
	}
	type group struct {
		parts [20]string
		flags []string
		width int
		bad   bool
	}
	groups := map[string]*group{}
	hints := map[string][]string{}
	for _, flag := range flags {
		lower := strings.ToLower(flag)
		width := len(lower) - 11
		if width != 48 && width != 20 && width != 5 || lower[0] != marker || lower[10] != '_' {
			continue
		}
		count := (nibbles + width - 1) / width
		index := int(lower[9] - 'a')
		if index < 0 || index >= count {
			continue
		}
		if _, err := hex.DecodeString(lower[1:9]); err != nil {
			continue
		}
		// A width-5 piece has an odd nibble count; pad only for syntax checking.
		if _, err := hex.DecodeString(lower[11:] + strings.Repeat("0", width%2)); err != nil {
			continue
		}
		id := lower[1:9] + "/" + strconv.Itoa(width)
		g := groups[id]
		if g == nil {
			g = &group{width: width}
			groups[id] = g
		}
		if g.parts[index] != "" && g.parts[index] != lower[11:] {
			g.bad = true
		}
		g.parts[index] = lower[11:]
		g.flags = append(g.flags, flag)
	}
	for id, g := range groups {
		if g.bad {
			continue
		}
		count := (nibbles + g.width - 1) / g.width
		payload := strings.Join(g.parts[:count], "")
		if len(payload) != count*g.width || payload[nibbles:] != strings.Repeat("0", len(payload)-nibbles) {
			continue
		}
		raw, err := hex.DecodeString(payload[:nibbles])
		if err != nil {
			continue
		}
		digest := sha256.Sum256(raw)
		if hex.EncodeToString(digest[:4]) != id[:8] {
			continue
		}
		payload = payload[:nibbles]
		hints[payload] = append(hints[payload], g.flags...)
	}
	return hints
}

func (e *Executor) rememberKeywordBytes(size int) {
	if size != 16 && size != 31 && size != 59 && size != 71 && size != 103 {
		return
	}
	e.mu.Lock()
	if e.keywordBytes == 0 || size < e.keywordBytes {
		e.keywordBytes = size
	}
	e.mu.Unlock()
}

func (e *Executor) preferredKeywordBytes() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keywordBytes
}
