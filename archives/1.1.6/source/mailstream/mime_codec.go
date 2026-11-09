package mailstream

import (
	"bytes"
	"encoding/ascii85"
)

// Mandatory wire format for every ITOM 1.1.6 mailbox publication.
// A 7-bit MIME envelope with 480-byte ASCII85 lines is safe for an IMAP
// APPEND literal without requiring the optional BINARY IMAP extension.
// Old ITOM3/Base64 messages are deliberately incompatible.
const ascii85MIMEHeader = "From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM4\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: 7bit\r\n\r\n"
const ascii85LineWidth = 480

func encodeMail(body []byte) []byte {
	var out bytes.Buffer
	maxEncoded := ascii85.MaxEncodedLen(len(body))
	out.Grow(len(ascii85MIMEHeader) + maxEncoded + (maxEncoded+ascii85LineWidth-1)/ascii85LineWidth*2)
	out.WriteString(ascii85MIMEHeader)
	encoded := make([]byte, maxEncoded)
	n := ascii85.Encode(encoded, body)
	encoded = encoded[:n]
	for len(encoded) > 0 {
		count := min(ascii85LineWidth, len(encoded))
		out.Write(encoded[:count])
		out.WriteString("\r\n")
		encoded = encoded[count:]
	}
	return out.Bytes()
}
