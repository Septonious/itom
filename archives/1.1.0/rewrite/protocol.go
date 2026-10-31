package mailstream

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"sync"

	"github.com/flynn/noise"
)

const (
	maxPayload = 64 << 10
	maxWire    = maxPayload + 128
	headerSize = 29
	hello      = byte(1)
	accept     = byte(2)
	sealed     = byte(3)
)

var (
	ErrProtocol = errors.New("MAILSTREAM_PROTOCOL")
	ErrAuth     = errors.New("MAILSTREAM_AUTHENTICATION")
	ErrClosed   = errors.New("MAILSTREAM_CLOSED")
	ErrCapacity = errors.New("MAILSTREAM_CAPACITY")
)

type SessionID [16]byte

func header(kind byte, id SessionID, sequence uint64) []byte {
	b := make([]byte, headerSize)
	copy(b, "ITM3")
	b[4] = kind
	copy(b[5:21], id[:])
	binary.BigEndian.PutUint64(b[21:], sequence)
	return b
}

func parseHeader(b []byte) (byte, SessionID, uint64, error) {
	var id SessionID
	if len(b) < headerSize || len(b) > maxWire || !bytes.Equal(b[:4], []byte("ITM3")) {
		return 0, id, 0, ErrProtocol
	}
	copy(id[:], b[5:21])
	if id == (SessionID{}) || b[4] < hello || b[4] > sealed {
		return 0, id, 0, ErrProtocol
	}
	return b[4], id, binary.BigEndian.Uint64(b[21:]), nil
}

type handshake struct {
	id    SessionID
	state *noise.HandshakeState
}

func newHandshake(psk []byte, identity string, id SessionID, client bool) (*handshake, error) {
	if len(psk) != 32 || len(identity) == 0 || len(identity) > 64 || id == (SessionID{}) {
		return nil, ErrProtocol
	}
	prologue := append([]byte("ITOM-mailstream-v3\x00"+identity+"\x00"), id[:]...)
	s, err := noise.NewHandshakeState(noise.Config{CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256), Pattern: noise.HandshakeNN, Initiator: client, PresharedKey: psk, PresharedKeyPlacement: 0, Prologue: prologue})
	if err != nil {
		return nil, ErrAuth
	}
	return &handshake{id, s}, nil
}

func initiate(psk []byte, identity string) (*handshake, []byte, error) {
	var id SessionID
	if _, err := rand.Read(id[:]); err != nil {
		return nil, nil, err
	}
	h, err := newHandshake(psk, identity, id, true)
	if err != nil {
		return nil, nil, err
	}
	b, _, _, err := h.state.WriteMessage(header(hello, id, 0), []byte("client"))
	if err != nil {
		return nil, nil, ErrAuth
	}
	return h, b, nil
}

func respond(psk []byte, identity string, b []byte) (*cipherPair, []byte, error) {
	kind, id, seq, err := parseHeader(b)
	if err != nil || kind != hello || seq != 0 {
		return nil, nil, ErrProtocol
	}
	h, err := newHandshake(psk, identity, id, false)
	if err != nil {
		return nil, nil, err
	}
	payload, _, _, err := h.state.ReadMessage(nil, b[headerSize:])
	if err != nil || !bytes.Equal(payload, []byte("client")) {
		return nil, nil, ErrAuth
	}
	reply, c2s, s2c, err := h.state.WriteMessage(header(accept, id, 0), []byte("server"))
	if err != nil {
		return nil, nil, ErrAuth
	}
	return newCipherPair(id, s2c, c2s), reply, nil
}

func (h *handshake) finish(b []byte) (*cipherPair, error) {
	kind, id, seq, err := parseHeader(b)
	if err != nil || kind != accept || id != h.id || seq != 0 {
		return nil, ErrProtocol
	}
	payload, c2s, s2c, err := h.state.ReadMessage(nil, b[headerSize:])
	if err != nil || !bytes.Equal(payload, []byte("server")) {
		return nil, ErrAuth
	}
	return newCipherPair(id, c2s, s2c), nil
}

// Each direction has a Noise split key and one monotonic sequence owner.
// Retransmission repeats the identical encrypted packet; it never encrypts
// different plaintext under a reused nonce. A new VPN session has new Noise
// ephemeral keys and a fresh random session ID. No cipher state is restored.
type cipherPair struct {
	id       SessionID
	mu       sync.Mutex
	send     noise.Cipher
	recv     noise.Cipher
	sequence uint64
}

func newCipherPair(id SessionID, send, recv *noise.CipherState) *cipherPair {
	return &cipherPair{id: id, send: send.Cipher(), recv: recv.Cipher()}
}
func (c *cipherPair) seal(body []byte) ([]byte, uint64, error) {
	if len(body) == 0 || len(body) > maxPayload {
		return nil, 0, ErrCapacity
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sequence == math.MaxUint64 {
		return nil, 0, ErrCapacity
	}
	seq := c.sequence
	c.sequence++
	h := header(sealed, c.id, seq)
	return c.send.Encrypt(h, seq, h, body), seq, nil
}
func (c *cipherPair) open(b []byte) ([]byte, uint64, error) {
	kind, id, seq, err := parseHeader(b)
	if err != nil || kind != sealed || id != c.id {
		return nil, 0, ErrProtocol
	}
	plain, err := c.recv.Decrypt(nil, seq, b[:headerSize], b[headerSize:])
	if err != nil || len(plain) == 0 || len(plain) > maxPayload {
		return nil, 0, ErrAuth
	}
	return plain, seq, nil
}

const (
	frameReady byte = iota + 1
	frameReadyAck
	frameOpen
	frameOpenOK
	frameData
	frameWindow
	frameFIN
	frameReset
	framePing
	framePong
	frameReceipt
)

type frame struct {
	kind   byte
	stream uint32
	offset uint64
	data   []byte
}

func (f frame) encode() ([]byte, error) {
	if f.kind < frameReady || f.kind > frameReceipt || len(f.data) > maxPayload-13 {
		return nil, ErrProtocol
	}
	b := make([]byte, 13+len(f.data))
	b[0] = f.kind
	binary.BigEndian.PutUint32(b[1:], f.stream)
	binary.BigEndian.PutUint64(b[5:], f.offset)
	copy(b[13:], f.data)
	return b, nil
}
func decodeFrame(b []byte) (frame, error) {
	if len(b) < 13 || b[0] < frameReady || b[0] > frameReceipt {
		return frame{}, ErrProtocol
	}
	return frame{kind: b[0], stream: binary.BigEndian.Uint32(b[1:]), offset: binary.BigEndian.Uint64(b[5:]), data: b[13:]}, nil
}
