package mailstream

import (
	"bytes"
	"crypto/rand"
	"encoding/ascii85"
	"testing"
)

// Exact released RC1 ASCII85 MIME line layout, used only as an immutable
// byte-level comparison. No extra encoder or protocol fallback ships.
func encodeMailRC116Reference(body []byte) []byte {
	const width = 480
	const block = width / 5 * 4
	out := []byte(ascii85MIMEHeader)
	for len(body) > 0 {
		n := min(block, len(body))
		buffer := make([]byte, ascii85.MaxEncodedLen(n))
		written := ascii85.Encode(buffer, body[:n])
		out = append(out, buffer[:written]...)
		out = append(out, '\r', '\n')
		body = body[n:]
	}
	return out
}

func TestRC117MIMEWrapReducesWireWithoutChangingContents(t *testing.T) {
	for _, size := range []int{0, 1, 23, 128, 1024, 4096, 16384, 64000} {
		packet := header(sealed, SessionID{1}, 5)
		payload := make([]byte, size)
		if _, err := rand.Read(payload); err != nil {
			t.Fatal(err)
		}
		packet = append(packet, payload...)
		old := encodeMailRC116Reference(packet)
		now := encodeMail(packet)
		if len(now) > len(old) {
			t.Fatalf("size=%d: increased wire from %d to %d bytes", size, len(old), len(now))
		}
		// With unchanged IMAP call count and scheduling, less data cannot
		// lower the serialization-only goodput ceiling.
		if float64(len(now)) > 1.25*float64(len(old)) {
			t.Fatalf("size=%d: theoretical throughput budget exceeded", size)
		}
		for _, line := range bytes.Split(now, []byte("\r\n")) {
			if len(line) > 998 {
				t.Fatalf("size=%d: MIME line exceeds 998 octets: %d", size, len(line))
			}
		}
		for _, wire := range [][]byte{old, now} {
			decoded, err := decodeMail(wire)
			if err != nil || len(decoded) != 1 || !bytes.Equal(decoded[0], packet) {
				t.Fatalf("size=%d: cross-version packet decode failed: %v", size, err)
			}
		}
	}
}

// At the production bulk sizes, check the real wire counts as well as the
// theoretical ceiling; do not claim throughput/RTT from this encoding test.
func TestRC117MIMEWrapBulkWireSavings(t *testing.T) {
	body := make([]byte, 256000)
	if _, err := rand.Read(body); err != nil {
		t.Fatal(err)
	}
	old, now := encodeMailRC116Reference(body), encodeMail(body)
	if len(now) >= len(old) {
		t.Fatalf("bulk wire did not shrink: RC1=%d RC2=%d", len(old), len(now))
	}
	t.Logf("measured MIME bytes: RC1=%d RC2=%d saved=%d (%.3f%%)", len(old), len(now),
		len(old)-len(now), 100*float64(len(old)-len(now))/float64(len(old)))
}
