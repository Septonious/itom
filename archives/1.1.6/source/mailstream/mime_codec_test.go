package mailstream

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/ascii85"
	"errors"
	"math"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

type legacyCodecPeer struct{ outgoing atomic.Bool }

func (p *legacyCodecPeer) setOptimizedMailCodec(on bool) { p.outgoing.Store(on) }
func (p *legacyCodecPeer) Send(context.Context, []byte, bool) error { return nil }
func (p *legacyCodecPeer) Receive() <-chan []byte { return nil }
func (p *legacyCodecPeer) Attach(context.Context, SessionID) error { return nil }
func (p *legacyCodecPeer) Close() error { return nil }

func rc116CodecTestCipher(t *testing.T) *cipherPair {
	t.Helper()
	key := make([]byte, 32)
	h, hello, err := initiate(key, "codec-test")
	if err != nil { t.Fatal(err) }
	_, reply, err := respond(key, "codec-test", hello)
	if err != nil { t.Fatal(err) }
	cipher, err := h.finish(reply)
	if err != nil { t.Fatal(err) }
	return cipher
}

func TestRC116CodecSevenBitRoundTripAndLegacyCompatibility(t *testing.T) {
	id := SessionID{1,2,3,4}
	for _, payloadSize := range []int{1, 79, 512, 4096, 65536, 200000} {
		raw := append(header(sealed, id, 1), make([]byte, payloadSize)...)
		if _, err := rand.Read(raw[headerSize:]); err != nil { t.Fatal(err) }
		legacy := encodeMail(raw)
		modern := encodeMailASCII85(raw)
		for _, body := range [][]byte{legacy, modern} {
			for _, b := range body {
				if b > 127 {
					t.Fatalf("message contains non-7-bit octet: 0x%x", b)
				}
			}
			all, err := decodeMail(body)
			if err != nil || len(all) != 1 || !bytes.Equal(all[0], raw) {
				t.Fatalf("payload=%d codec=%q: packets=%d err=%v", payloadSize, body[:min(len(body),64)], len(all), err)
			}
		}
		if len(raw) >= 4096 && len(modern)*100 >= len(legacy)*95 {
			t.Fatalf("payload=%d: ASCII85 did not reduce MIME bytes by at least five percent: legacy=%d optimized=%d", payloadSize, len(legacy), len(modern))
		}
	}
}

func TestRC116OptimizedMIMEEnvelopeLineLengthAndTamperReject(t *testing.T) {
	raw := append(header(sealed, SessionID{1}, 42), bytes.Repeat([]byte{0x7f}, 32768)...)
	encoded := encodeMailASCII85(raw)
	parts := bytes.Split(encoded, []byte("\r\n\r\n"))
	if len(parts) != 2 { t.Fatal("unexpected MIME envelope header split") }
	for _, line := range bytes.Split(parts[1], []byte("\r\n")) {
		if len(line) > 480 {
			t.Fatalf("ASCII85 encoded line exceeds bound: %d", len(line))
		}
	}
	if !preferASCII85(len(raw)) {
		t.Fatal("large payload should choose fewer encoded bytes")
	}
	if preferASCII85(0) || preferASCII85(1) {
		t.Fatal("tiny messages must preserve legacy base64 when smaller")
	}
	invalid := bytes.Replace(encoded, []byte("Content-Transfer-Encoding: x-itom-ascii85"), []byte("Content-Transfer-Encoding: x-untrusted"), 1)
	if _, err := decodeMail(invalid); !errors.Is(err, ErrProtocol) {
		t.Fatalf("unknown MIME encoding was accepted: %v", err)
	}
	broken := bytes.Replace(encoded, []byte("Content-Transfer-Encoding: x-itom-ascii85\r\n\r\n"), []byte("Content-Transfer-Encoding: x-itom-ascii85\r\n\r\n~"), 1)
	if _, err := decodeMail(broken); err == nil {
		t.Fatal("invalid ASCII85 body accepted")
	}
	if n := ascii85.MaxEncodedLen(200000); n > math.MaxInt32 {
		t.Fatalf("unreasonable max ASCII85 wire size %d", n)
	}
}

func TestRC116OptimizedMIMENegotiatesOnlyWithUpgradedPeer(t *testing.T) {
	cipher := rc116CodecTestCipher(t)
	legacy := &legacyCodecPeer{}
	ctx := context.Background()
	newClient := &Session{ctx: ctx, link: legacy, cipher: cipher, changed: make(chan struct{}), wake: make(chan struct{}, 1), pending: make(map[uint64]*pendingPacket), closedStreams: make(map[uint32]*terminalStream)}
	if handled, err := newClient.applyMailCodecNegotiation(frame{kind: framePong, offset: mailCodecOfferNonce}); handled || err != nil || legacy.outgoing.Load() {
		t.Fatalf("a legacy echoed PONG must leave Base64 unchanged: handled=%t err=%v", handled, err)
	}
	if handled, err := newClient.applyMailCodecNegotiation(frame{kind: framePong, offset: mailCodecSupportedNonce}); !handled || err != nil || !legacy.outgoing.Load() {
		t.Fatalf("upgraded peer did not activate Base85: handled=%t err=%v", handled, err)
	}
	if len(newClient.pending) != 1 {
		t.Fatalf("client did not queue authenticated activation: pending=%d", len(newClient.pending))
	}
	serverPeer := &legacyCodecPeer{}
	server := &Session{ctx: ctx, server: true, link: serverPeer, cipher: cipher, changed: make(chan struct{}), wake: make(chan struct{}, 1), pending: make(map[uint64]*pendingPacket), closedStreams: make(map[uint32]*terminalStream)}
	if handled, err := server.applyMailCodecNegotiation(frame{kind: framePing, offset: mailCodecOfferNonce}); !handled || err != nil || serverPeer.outgoing.Load() {
		t.Fatalf("capability offer must stay legacy Base64: handled=%t err=%v", handled, err)
	}
	if handled, err := server.applyMailCodecNegotiation(frame{kind: framePing, offset: mailCodecActivateNonce}); !handled || err != nil || !serverPeer.outgoing.Load() {
		t.Fatalf("activation not applied: handled=%t err=%v", handled, err)
	}
	serverPeer.setOptimizedMailCodec(false)
	if serverPeer.outgoing.Load() { t.Fatal("server codec did not reset on session replacement") }
}

func BenchmarkRC116MIMEEncoding(b *testing.B) {
	for _, size := range []int{512, 4096, 65536} {
		body := make([]byte, size)
		rand.Read(body)
		for _, variant := range []struct{name string; fn func([]byte) []byte}{
			{"base64", encodeMail}, {"ascii85", encodeMailASCII85},
		} {
			b.Run(variant.name+"/"+fmtSize(size), func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				b.ResetTimer()
				for i:=0; i<b.N; i++ { _ = variant.fn(body) }
			})
		}
	}
}
func fmtSize(n int) string {
	if n>=1024 { return strconv.Itoa(n/1024)+"KiB" }
	return strconv.Itoa(n)+"B"
}

var _ = time.Millisecond
