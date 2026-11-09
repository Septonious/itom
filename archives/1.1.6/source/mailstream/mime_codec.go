package mailstream

import (
	"bytes"
	"encoding/ascii85"
)

// ITOM's established Base64 MIME format remains the default and the
// compatibility fallback for every unauthenticated handshake message.
//
// ASCII85 is 7-bit textual MIME, preserving RFC3501 APPEND literal safety
// without requiring the optional IMAP BINARY extension. At steady state
// it uses at most five output characters per four input bytes rather than
// Base64's four per three. Encoded lines are capped at 480 bytes, below the
// 998-byte transport line limit. The CTE is private and MUST be decoded
// only by an explicitly upgraded ITOM receiver.
func encodeMailASCII85(body []byte) []byte {
	const lineWidth = 480
	var out bytes.Buffer
	out.Grow(180 + ascii85.MaxEncodedLen(len(body)) + ascii85.MaxEncodedLen(len(body))/lineWidth*2 + 4)
	out.WriteString("From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM3\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: x-itom-ascii85\r\n\r\n")
	encoded := make([]byte, ascii85.MaxEncodedLen(len(body)))
	n := ascii85.Encode(encoded, body)
	encoded = encoded[:n]
	for len(encoded) > 0 {
		count := min(lineWidth, len(encoded))
		out.Write(encoded[:count])
		out.WriteString("\r\n")
		encoded = encoded[count:]
	}
	return out.Bytes()
}

// A per-link mode is automatically negotiated *after* the authenticated
// READY/READY_ACK. All hello, accept and sequence-zero recovery traffic
// remains Base64 even after activation. Normal sessions start in Base64
// and an old peer can continue using it indefinitely.
func (l *IMAPLink) setOptimizedMailCodec(enabled bool) {
	l.optimizedMIME.Store(enabled)
}
