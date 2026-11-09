package mailstream

import (
	"bytes"
	"crypto/rand"
	"encoding/ascii85"
	"encoding/base64"
	"math"
	"testing"
)

func TestRC116MandatorySevenBitMIMECiphertextRoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, 23, 128, 1024, 4096, 65536} {
		var session SessionID
		session[0] = 42
		packet := header(sealed, session, 1)
		payload := make([]byte, size)
		if _, err := rand.Read(payload); err != nil { t.Fatal(err) }
		packet = append(packet, payload...)
		encoded := encodeMail(packet)
		if !bytes.Contains(encoded, []byte("Content-Transfer-Encoding: 7bit\r\n")) ||
			!bytes.Contains(encoded, []byte("Subject: ITOM4\r\n")) {
			t.Fatalf("not mandatory 7bit/ITOM4 for size=%d", size)
		}
		for _, c := range encoded {
			if c >= 128 || c == 0 {
				t.Fatalf("MIME contains a non-7-bit octet: %#x", c)
			}
		}
		for _, line := range bytes.Split(encoded, []byte("\r\n")) {
			if len(line) > 998 { t.Fatalf("MIME line exceeds RFC limit: %d", len(line)) }
		}
		got, err := decodeMail(encoded)
		if err != nil || len(got) != 1 || !bytes.Equal(got[0], packet) {
			t.Fatalf("MIME round-trip size=%d: count=%d err=%v", size, len(got), err)
		}
	}
}

func TestRC116MandatoryLargeBatchedMIMEEnvelope(t *testing.T) {
	id := SessionID{1}
	var packets [][]byte
	for i := 0; i < 3; i++ {
		packet := append(header(sealed, id, uint64(i+1)), make([]byte, 60000)...)
		if _, err := rand.Read(packet[headerSize:]); err != nil { t.Fatal(err) }
		packets = append(packets, packet)
	}
	plain := packMail(packets)
	if len(plain) <= 160000 || len(plain) >= maxMailPlain { t.Fatalf("bad MIME batch length %d", len(plain)) }
	got, err := decodeMail(encodeMail(plain))
	if err != nil || len(got) != len(packets) { t.Fatalf("batch decode failed: count=%d err=%v", len(got), err) }
	for i := range packets {
		if !bytes.Equal(got[i], packets[i]) { t.Fatalf("batch entry %d corrupted", i) }
	}
}

func TestRC116MandatoryMIMERejectsLegacyAndUnknownEncoding(t *testing.T) {
	var session SessionID
	session[0] = 1
	packet := append(header(sealed, session, 1), []byte("authenticated-in-session")...)
	current := encodeMail(packet)
	for _, prior := range [][]byte{
		bytes.Replace(current, []byte("Subject: ITOM4"), []byte("Subject: ITOM3"), 1),
		bytes.Replace(current, []byte("Content-Transfer-Encoding: 7bit"), []byte("Content-Transfer-Encoding: base64"), 1),
		bytes.Replace(current, []byte("Content-Transfer-Encoding: 7bit"), []byte("Content-Transfer-Encoding: x-itom-ascii85"), 1),
		bytes.Replace(current, []byte("\r\n\r\n"), []byte("\r\n\r\n~"), 1),
	} {
		if _, err := decodeMail(prior); err == nil { t.Fatal("invalid or legacy MIME accepted") }
	}
	batch := packMail([][]byte{packet, append(header(sealed, session, 2), []byte("second")...)})
	got, err := decodeMail(encodeMail(batch))
	if err != nil || len(got) != 2 { t.Fatalf("batched round-trip count=%d err=%v",len(got),err) }
}

func TestRC116MandatoryASCII85EncodingEfficiency(t *testing.T) {
	for _, size := range []int{512, 4096, 65536, 200000} {
		body := make([]byte, size)
		if _, err := rand.Read(body); err != nil { t.Fatal(err) }
		wire := encodeMail(body)
		oldBase64 := len("From: transport@itom.invalid\r\nTo: transport@itom.invalid\r\nSubject: ITOM3\r\nMIME-Version: 1.0\r\nContent-Type: application/octet-stream\r\nContent-Transfer-Encoding: base64\r\n\r\n") + base64.StdEncoding.EncodedLen(size)
		oldBase64 += 2*((base64.StdEncoding.EncodedLen(size)+75)/76)
		if size >= 4096 && float64(len(wire))/float64(oldBase64) >= 0.95 {
			t.Fatalf("ASCII85 must save >5%% of MIME bytes: size=%d new=%d old=%d",size,len(wire),oldBase64)
		}
		if size == 65536 {
			throughput := 20 * float64(size)/float64(len(wire))
			t.Logf("20 Mbps raw network theoretical ceiling, encoding only: %.2f Mbps payload; wire=%.3fx; legacy=%d bytes; ASCII85=%d bytes",throughput,float64(len(wire))/float64(size),oldBase64,len(wire))
			if math.IsNaN(throughput) || throughput <= 15 { t.Fatal("encoded payload efficiency unacceptable") }
		}
	}
}

func BenchmarkRC116MandatoryMIMEEncoding(b *testing.B) {
	for _, size := range []int{512, 4096, 65536} {
		body := make([]byte,size)
		if _,err := rand.Read(body); err != nil { b.Fatal(err) }
		b.Run(stringSize(size),func(b *testing.B){
			b.SetBytes(int64(size)); b.ReportAllocs()
			for i:=0;i<b.N;i++ { _ = encodeMail(body) }
		})
	}
}

func stringSize(n int) string {
	if n >= 1024 { return "large" }
	return "small"
}

var _ = ascii85.MaxEncodedLen
