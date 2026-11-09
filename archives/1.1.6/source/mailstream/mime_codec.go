package mailstream

import "encoding/ascii85"

// Mandatory format for every ITOM 1.1.6 IMAP APPEND: 7-bit MIME and
// ASCII85 ciphertext. No Base64 encoder, dual format, or negotiation.
// The 960-octet line is below the mail-format 998-octet limit and
// avoids half the wrapping overhead of RC1's 480-octet lines.
// No new coalescing delay, negotiation, encoding, or protocol version.
const ascii85MIMEHeader = "From: t@itom.invalid\r\nTo: t@itom.invalid\r\nSubject: ITOM4\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: 7bit\r\n\r\n"

const (
	ascii85LineWidth  = 960
	ascii85PlainBlock = ascii85LineWidth / 5 * 4
)

// Encode straight into one allocated MIME buffer. Raw blocks are divisible
// by four, so independently encoding each block is equivalent to encoding
// the entire stream. The final block may be shorter.
func encodeMail(body []byte) []byte {
	maxEncoded := ascii85.MaxEncodedLen(len(body))
	capacity := len(ascii85MIMEHeader) + maxEncoded + 2*((maxEncoded+ascii85LineWidth-1)/ascii85LineWidth)
	out := make([]byte, 0, capacity)
	out = append(out, ascii85MIMEHeader...)
	for len(body) > 0 {
		n := min(ascii85PlainBlock, len(body))
		begin := len(out)
		out = out[:begin+ascii85.MaxEncodedLen(n)]
		written := ascii85.Encode(out[begin:], body[:n])
		out = out[:begin+written]
		out = append(out, '\r', '\n')
		body = body[n:]
	}
	return out
}
