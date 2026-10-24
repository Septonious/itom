// Package secure owns Noise state. It exports no keys or raw CipherState.
// Ready/epoch activation belongs to the session state machine, not this codec.
package secure

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"sync"

	"github.com/flynn/noise"
	"itom.local/core/wire"
)

var (
	ErrAuth     = errors.New("AUTHENTICATION")
	ErrContext  = errors.New("CRYPTO_CONTEXT")
	ErrState    = errors.New("HANDSHAKE_STATE")
	ErrKeyLimit = errors.New("KEY_LIMIT")
)

const (
	HardBytes       uint64 = 8 << 30
	SoftBytes       uint64 = 4 << 30
	HardRecords     uint64 = 1 << 21
	SoftRecords     uint64 = 1 << 20
	ControlHeadroom uint64 = 1 << 20
)

type Binding struct {
	Server     wire.ID
	User       string
	Pair       wire.ID
	Session    wire.ID
	Handshake  wire.ID
	Epoch      uint32
	ConfigHash [32]byte
}

func (b Binding) prologue() ([]byte, error) {
	if b.Server == (wire.ID{}) || b.Pair == (wire.ID{}) || b.Session == (wire.ID{}) ||
		b.Handshake == (wire.ID{}) || b.Epoch == 0 || len(b.User) != 8 ||
		b.User[0] == '0' || b.ConfigHash == ([32]byte{}) {
		return nil, ErrContext
	}
	for _, c := range []byte(b.User) {
		if c < '0' || c > '9' {
			return nil, ErrContext
		}
	}
	p := append([]byte("ITOM/2/Noise_NNpsk0_25519_AESGCM_SHA256"), 0)
	p = append(p, b.Server[:]...)
	p = append(p, []byte(b.User)...)
	p = append(p, b.Pair[:]...)
	p = append(p, b.Session[:]...)
	var epoch [4]byte
	binary.BigEndian.PutUint32(epoch[:], b.Epoch)
	p = append(p, epoch[:]...)
	p = append(p, b.Handshake[:]...)
	p = append(p, b.ConfigHash[:]...)
	return p, nil
}

type Handshake struct {
	sessionV2   bool
	binding     Binding
	initiator   bool
	state       *noise.HandshakeState
	step        int
	failed      bool
	lastRead    []byte
	lastPayload []byte
	lastWrite   []byte
	epoch       *Epoch
}

// NewHandshake always uses the OS CSPRNG. Deterministic entropy is restricted
// to package-local tests; production callers cannot inject it.
func NewHandshake(b Binding, psk []byte, initiator bool) (*Handshake, error) {
	return newHandshake(b, psk, initiator, rand.Reader)
}
func newHandshake(b Binding, psk []byte, initiator bool, entropy io.Reader) (*Handshake, error) {
	return newHandshakeDomain(b, psk, initiator, entropy, false)
}

// NewSessionHandshake creates independent keys for the replacement transport.
// The v2 domain is mixed into Noise before any key material is derived.
func NewSessionHandshake(b Binding, psk []byte, initiator bool) (*Handshake, error) {
	return newHandshakeDomain(b, psk, initiator, rand.Reader, true)
}
func newHandshakeDomain(b Binding, psk []byte, initiator bool, entropy io.Reader, sessionV2 bool) (*Handshake, error) {
	prologue, err := b.prologue()
	if sessionV2 {
		prologue = append([]byte("ITOM/session-transport/v2\x00"), prologue...)
	}
	if err != nil {
		return nil, err
	}
	if len(psk) != 32 {
		return nil, ErrContext
	}
	state, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherAESGCM, noise.HashSHA256),
		Pattern:     noise.HandshakeNN, Initiator: initiator, Prologue: prologue,
		PresharedKey: append([]byte(nil), psk...), PresharedKeyPlacement: 0, Random: entropy,
	})
	if err != nil {
		return nil, err
	}
	return &Handshake{binding: b, initiator: initiator, state: state, sessionV2: sessionV2}, nil
}
func (h *Handshake) split(a, b *noise.CipherState) {
	if a == nil || b == nil {
		return
	}
	tx, rx := a, b
	direction := wire.C2S
	if !h.initiator {
		tx, rx = b, a
		direction = wire.S2C
	}
	h.epoch = &Epoch{binding: h.binding, direction: direction, tx: tx, rx: rx, sessionV2: h.sessionV2}
}

// Write retransmits immutable cached bytes on a duplicate call. Callers must
// keep the same handshake payload; a changed payload under the same ID fails.
func (h *Handshake) Write(payload []byte) ([]byte, error) {
	if h.failed || len(payload) != 0 {
		return nil, ErrState
	}
	expected := 0
	if !h.initiator {
		expected = 1
	}
	if h.step > expected {
		if bytes.Equal(payload, h.lastPayload) && h.lastWrite != nil {
			return append([]byte(nil), h.lastWrite...), nil
		}
		return nil, ErrState
	}
	if h.step != expected {
		return nil, ErrState
	}
	msg, a, b, err := h.state.WriteMessage(nil, payload)
	if err != nil {
		h.failed = true
		return nil, ErrAuth
	}
	if len(msg) > wire.MaxHandshake {
		h.failed = true
		return nil, wire.ErrLimit
	}
	h.lastPayload = append([]byte(nil), payload...)
	h.lastWrite = append([]byte(nil), msg...)
	h.step++
	h.split(a, b)
	return msg, nil
}
func (h *Handshake) Read(msg []byte) error {
	if h.failed || len(msg) == 0 || len(msg) > wire.MaxHandshake {
		return ErrState
	}
	expected := 1
	if !h.initiator {
		expected = 0
	}
	if h.step > expected {
		if bytes.Equal(msg, h.lastRead) && h.lastRead != nil {
			return nil
		}
		return ErrState
	}
	if h.step != expected {
		return ErrState
	}
	payload, a, b, err := h.state.ReadMessage(nil, msg)
	if err != nil {
		h.failed = true
		return ErrAuth
	}
	// This milestone binds negotiation through ConfigHash, not an ad hoc
	// unvalidated handshake payload.
	if len(payload) != 0 {
		h.failed = true
		return ErrContext
	}
	h.lastRead = append([]byte(nil), msg...)
	h.step++
	h.split(a, b)
	return nil
}
func (h *Handshake) Epoch() (*Epoch, error) {
	if h.failed || h.epoch == nil || h.step != 2 {
		return nil, ErrState
	}
	return h.epoch, nil
}

type Epoch struct {
	sessionV2  bool
	binding    Binding
	direction  wire.Direction
	tx, rx     *noise.CipherState
	txMu, rxMu sync.Mutex
	replay     replayWindow
	txBytes    uint64
	rxBytes    uint64
	rxFailures uint32
}

func (e *Epoch) NeedsRekey() bool {
	e.txMu.Lock()
	defer e.txMu.Unlock()
	return e.txBytes >= SoftBytes || e.tx.Nonce() >= SoftRecords
}
func (e *Epoch) Seal(f wire.Frame) ([]byte, error) {
	if e.sessionV2 {
		return nil, ErrContext
	}
	inner, err := f.MarshalBinary()
	if err != nil {
		return nil, err
	}
	e.txMu.Lock()
	defer e.txMu.Unlock()
	n := uint64(wire.HeaderSize + len(inner))
	if e.tx.Nonce() >= HardRecords || e.txBytes > HardBytes || n > HardBytes-e.txBytes {
		return nil, ErrKeyLimit
	}
	if f.Type == wire.Data && (e.tx.Nonce() >= HardRecords-64 ||
		e.txBytes+n > HardBytes-ControlHeadroom) {
		return nil, ErrKeyLimit
	}
	h := wire.Header{Kind: wire.Record, Pair: e.binding.Pair, Session: e.binding.Session,
		Epoch: e.binding.Epoch, Direction: e.direction, Counter: e.tx.Nonce(),
		BodyLength: uint32(n + wire.TagSize)}
	header, err := h.MarshalBinary()
	if err != nil {
		return nil, err
	}
	plain := make([]byte, 0, int(n))
	plain = append(plain, header...)
	plain = append(plain, inner...)
	ciphertext, err := e.tx.Encrypt(nil, nil, plain)
	clear(plain)
	if err != nil {
		return nil, ErrKeyLimit
	}
	e.txBytes += n
	result := append(header, ciphertext...)
	return result, nil
}
func (e *Epoch) Open(packet []byte) (wire.Frame, error) { return e.open(packet, false) }

// OpenControl rejects non-control records before committing the replay counter.
// An unauthenticated mailbox routing marker cannot consume a valid DATA record.
func (e *Epoch) OpenControl(packet []byte) (wire.Frame, error) { return e.open(packet, true) }
func (e *Epoch) open(packet []byte, controlOnly bool) (wire.Frame, error) {
	if e.sessionV2 {
		return wire.Frame{}, ErrContext
	}
	if len(packet) < wire.HeaderSize || len(packet) > wire.MaxRecord {
		return wire.Frame{}, wire.ErrLimit
	}
	h, err := wire.ParseHeader(packet[:wire.HeaderSize])
	if err != nil {
		return wire.Frame{}, err
	}
	if h.Kind != wire.Record || int(h.BodyLength) != len(packet)-wire.HeaderSize ||
		h.Pair != e.binding.Pair || h.Session != e.binding.Session || h.Epoch != e.binding.Epoch ||
		h.Direction != e.direction.Reverse() || h.Counter >= HardRecords {
		return wire.Frame{}, ErrContext
	}
	e.rxMu.Lock()
	defer e.rxMu.Unlock()
	if e.rxFailures >= 1024 {
		return wire.Frame{}, ErrKeyLimit
	}
	if err = e.replay.check(h.Counter); err != nil {
		return wire.Frame{}, err
	}
	e.rx.SetNonce(h.Counter)
	plain, err := e.rx.Decrypt(nil, nil, packet[wire.HeaderSize:])
	if err != nil {
		e.rxFailures++
		return wire.Frame{}, ErrAuth
	}
	defer clear(plain)
	if len(plain) < wire.HeaderSize || !bytes.Equal(plain[:wire.HeaderSize], packet[:wire.HeaderSize]) {
		e.rxFailures++
		return wire.Frame{}, ErrAuth
	}
	if e.rxBytes > HardBytes || uint64(len(plain)) > HardBytes-e.rxBytes {
		return wire.Frame{}, ErrKeyLimit
	}
	frame, err := wire.ParseFrame(plain[wire.HeaderSize:])
	if err != nil {
		return wire.Frame{}, err
	}
	if controlOnly {
		if _, err = wire.DecodeControl(frame); err != nil {
			clear(frame.Payload)
			return wire.Frame{}, err
		}
	}
	e.rxBytes += uint64(len(plain))
	e.replay.commit(h.Counter)
	return frame, nil
}

// Context returns a copy of the public authenticated session binding, not keys.
func (e *Epoch) Context() Binding { return e.binding }

// SessionDirection exposes immutable role/domain metadata, never keys or Noise
// state. Transport owners must not attach a legacy epoch or opposite endpoint's
// sender role to the replacement session merely because public bindings match.
func (e *Epoch) SessionDirection() (wire.Direction, bool) { return e.direction, e.sessionV2 }
