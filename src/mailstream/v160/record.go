package v160

import (
	"bytes"
	"encoding/binary"
	"math"
	"sync"

	"github.com/flynn/noise"
)

const (
	MaxPlaintext   = 64 << 10
	RecordOverhead = 24 // uint64 sequence + full Poly1305 tag
	MaxRecord      = MaxPlaintext + RecordOverhead
	MaxContainer   = 256 << 10
	MaxFrames      = 128
	MaxRecords     = 128
	MaxData        = 32 << 10
	ReplaySpan     = 4096
)

// Kind-specific fields avoid carrying an offset or stream ID on every frame.
// Session control payloads have bounded length; their operation-specific
// validation belongs to the corresponding state machine, before applying them.
type Kind uint8

const (
	Ready Kind = iota + 1
	ReadyAck
	Open
	OpenOK
	Data
	Window
	FIN
	Reset
	Ping
	Pong
	Receipt
	Gap
	Grant
	GrantAck
	SlotChange
	SlotAck
	Retire
	Datagram
	SequenceFloor
)

type Frame struct {
	Kind          Kind
	Stream        uint32
	Offset, Value uint64
	Payload       []byte
}

const (
	fieldStream = 1 << iota
	fieldOffset
	fieldValue
	fieldPayload
)

func schema(k Kind) (int, int) {
	switch k {
	case Ready:
		return 0, 0
	case ReadyAck:
		return fieldPayload, 4096
	case Open:
		return fieldStream | fieldPayload, 1024
	case OpenOK, Reset:
		return fieldStream, 0
	case Data:
		return fieldStream | fieldOffset | fieldPayload, MaxData
	case Window, Gap:
		return fieldStream | fieldOffset | fieldValue, 0
	case FIN:
		return fieldStream | fieldOffset, 0
	case Ping, Pong, GrantAck, SlotAck, Retire, SequenceFloor:
		return fieldOffset, 0
	case Receipt:
		return fieldPayload, 8192
	case Grant, SlotChange:
		return fieldOffset | fieldPayload, 4096
	case Datagram:
		return fieldStream | fieldPayload, 65507
	}
	return -1, 0
}
func (f Frame) valid() bool {
	flags, limit := schema(f.Kind)
	if flags < 0 || len(f.Payload) > limit {
		return false
	}
	if flags&fieldStream != 0 {
		if f.Stream == 0 {
			return false
		}
	} else if f.Stream != 0 {
		return false
	}
	if flags&fieldOffset == 0 && f.Offset != 0 || flags&fieldValue == 0 && f.Value != 0 || flags&fieldPayload == 0 && len(f.Payload) != 0 {
		return false
	}
	switch f.Kind {
	case Data:
		return len(f.Payload) > 0 && f.Offset <= math.MaxUint64-uint64(len(f.Payload))
	case Open, Datagram, Receipt, Grant, SlotChange:
		return len(f.Payload) > 0
	case Window:
		return f.Value >= f.Offset
	case Gap:
		return f.Value > f.Offset
	}
	return true
}

// readVar rejects redundant, truncated and overflowing encodings.
func readVar(b *[]byte) (uint64, error) {
	v, n := binary.Uvarint(*b)
	if n <= 0 {
		return 0, ErrProtocol
	}
	var canonical [10]byte
	if binary.PutUvarint(canonical[:], v) != n {
		return 0, ErrProtocol
	}
	*b = (*b)[n:]
	return v, nil
}
func EncodeFrames(frames []Frame) ([]byte, error) {
	if len(frames) == 0 || len(frames) > MaxFrames {
		return nil, ErrCapacity
	}
	var b []byte
	for _, f := range frames {
		if !f.valid() {
			return nil, ErrProtocol
		}
		flags, _ := schema(f.Kind)
		b = append(b, byte(f.Kind))
		if flags&fieldStream != 0 {
			b = binary.AppendUvarint(b, uint64(f.Stream))
		}
		if flags&fieldOffset != 0 {
			b = binary.AppendUvarint(b, f.Offset)
		}
		if flags&fieldValue != 0 {
			b = binary.AppendUvarint(b, f.Value)
		}
		if flags&fieldPayload != 0 {
			b = binary.AppendUvarint(b, uint64(len(f.Payload)))
			if len(b)+len(f.Payload) > MaxPlaintext {
				return nil, ErrCapacity
			}
			b = append(b, f.Payload...)
		}
		if len(b) > MaxPlaintext {
			return nil, ErrCapacity
		}
	}
	return b, nil
}

// DecodeFrames borrows the authenticated plaintext until its owner releases it.
// No length can drive allocation before it is validated against the input.
func DecodeFrames(b []byte) ([]Frame, error) {
	if len(b) == 0 || len(b) > MaxPlaintext {
		return nil, ErrProtocol
	}
	var frames []Frame
	for len(b) > 0 {
		if len(frames) == MaxFrames {
			return nil, ErrCapacity
		}
		f := Frame{Kind: Kind(b[0])}
		b = b[1:]
		flags, limit := schema(f.Kind)
		if flags < 0 {
			return nil, ErrProtocol
		}
		var err error
		if flags&fieldStream != 0 {
			v, e := readVar(&b)
			if e != nil || v > math.MaxUint32 {
				return nil, ErrProtocol
			}
			f.Stream = uint32(v)
		}
		if flags&fieldOffset != 0 {
			f.Offset, err = readVar(&b)
			if err != nil {
				return nil, err
			}
		}
		if flags&fieldValue != 0 {
			f.Value, err = readVar(&b)
			if err != nil {
				return nil, err
			}
		}
		if flags&fieldPayload != 0 {
			n, e := readVar(&b)
			if e != nil || n > uint64(limit) || n > uint64(len(b)) {
				return nil, ErrProtocol
			}
			f.Payload = b[:int(n)]
			b = b[int(n):]
		}
		if !f.valid() {
			return nil, ErrProtocol
		}
		frames = append(frames, f)
	}
	return frames, nil
}
func Pack(records [][]byte) ([]byte, error) {
	if len(records) == 0 || len(records) > MaxRecords {
		return nil, ErrCapacity
	}
	var b []byte
	for _, r := range records {
		if len(r) <= RecordOverhead || len(r) > MaxRecord {
			return nil, ErrProtocol
		}
		b = binary.AppendUvarint(b, uint64(len(r)))
		if len(b)+len(r) > MaxContainer {
			return nil, ErrCapacity
		}
		b = append(b, r...)
	}
	return b, nil
}
func Unpack(b []byte) ([][]byte, error) {
	if len(b) == 0 || len(b) > MaxContainer {
		return nil, ErrProtocol
	}
	var records [][]byte
	for len(b) > 0 {
		if len(records) == MaxRecords {
			return nil, ErrCapacity
		}
		n, e := readVar(&b)
		if e != nil || n <= RecordOverhead || n > MaxRecord || n > uint64(len(b)) {
			return nil, ErrProtocol
		}
		records = append(records, b[:int(n)])
		b = b[int(n):]
	}
	return records, nil
}

// Each direction uses the same transcript binding but a distinct key and AAD
// label. AAD has no account, UID or slot, so repair repeats immutable wire bytes.
// The sequence-space fence is owned by the reliability layer; no record can be
// emitted beyond the advertised span until earlier reliable records retire.
type RecordCipher struct {
	mu               sync.Mutex
	send, recv       noise.Cipher
	sendAAD, recvAAD [33]byte
	next, floor      uint64
}

func newRecordCipher(send, recv *noise.CipherState, binding []byte, client bool) *RecordCipher {
	c := &RecordCipher{send: send.Cipher(), recv: recv.Cipher()}
	copy(c.sendAAD[:32], binding)
	copy(c.recvAAD[:32], binding)
	if client {
		c.sendAAD[32] = 1
		c.recvAAD[32] = 2
	} else {
		c.sendAAD[32] = 2
		c.recvAAD[32] = 1
	}
	return c
}
func associated(prefix [33]byte, seq uint64) [41]byte {
	var a [41]byte
	copy(a[:], prefix[:])
	binary.BigEndian.PutUint64(a[33:], seq)
	return a
}
func (c *RecordCipher) Seal(frames []Frame) ([]byte, uint64, error) {
	p, e := EncodeFrames(frames)
	if e != nil {
		return nil, 0, e
	}
	return c.sealPlain(p, false)
}

func (c *RecordCipher) nextSequence() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.next
}

func (c *RecordCipher) sealPlain(p []byte, feedback bool) ([]byte, uint64, error) {
	if len(p) == 0 || len(p) > MaxPlaintext {
		return nil, 0, ErrCapacity
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next == math.MaxUint64 || !feedback && c.next-c.floor >= ReplaySpan {
		return nil, 0, ErrCapacity
	}
	seq := c.next
	c.next++
	head := make([]byte, 8, 8+len(p)+16)
	binary.BigEndian.PutUint64(head, seq)
	a := associated(c.sendAAD, seq)
	return c.send.Encrypt(head, seq, a[:], p), seq, nil
}

// Open authenticates first and has no replay side effect. The single receive
// owner must commit replay state only after atomic acceptance of all frames.
func (c *RecordCipher) Open(wire []byte) ([]Frame, uint64, error) {
	if len(wire) <= RecordOverhead || len(wire) > MaxRecord {
		return nil, 0, ErrProtocol
	}
	seq := binary.BigEndian.Uint64(wire[:8])
	if seq == math.MaxUint64 {
		return nil, 0, ErrProtocol
	}
	a := associated(c.recvAAD, seq)
	p, e := c.recv.Decrypt(nil, seq, a[:], wire[8:])
	if e != nil {
		return nil, 0, ErrProtocol
	}
	f, e := DecodeFrames(p)
	return f, seq, e
}

// AdvanceSendFloor is only called by the reliable-record owner after verifying
// all required receipts below floor. It must not be called on APPEND success.
func (c *RecordCipher) AdvanceSendFloor(floor uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if floor < c.floor || floor > c.next {
		return ErrState
	}
	c.floor = floor
	return nil
}

// Handshake uses Noise NNpsk0; the version/user/session and limits are bound in
// the prologue. Fixed initial capabilities avoid silent protocol downgrade.
// READY/READY_ACK are separate encrypted frames and must gate admission.
var helloPrefix = []byte{'I', 'T', '1', '6', 1}
var acceptPrefix = []byte{'I', 'T', '1', '6', 2}

type Initiator struct {
	state    *noise.HandshakeState
	finished bool
}

func handshakeState(key Key, psk []byte, client bool) (*noise.HandshakeState, error) {
	if !key.Valid() || len(psk) != 32 {
		return nil, ErrProtocol
	}
	p := append([]byte("ITOM/1.6.0\x00"), []byte(key.User)...)
	p = binary.BigEndian.AppendUint32(p, uint32(key.Session))
	p = binary.BigEndian.AppendUint32(p, MaxPlaintext)
	p = binary.BigEndian.AppendUint32(p, ReplaySpan)
	return noise.NewHandshakeState(noise.Config{CipherSuite: noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256), Pattern: noise.HandshakeNN, Initiator: client, PresharedKey: psk, PresharedKeyPlacement: 0, Prologue: p})
}
func Begin(key Key, psk []byte) (*Initiator, []byte, error) {
	s, e := handshakeState(key, psk, true)
	if e != nil {
		return nil, nil, e
	}
	b, _, _, e := s.WriteMessage(bytes.Clone(helloPrefix), []byte("client"))
	if e != nil {
		return nil, nil, e
	}
	return &Initiator{state: s}, b, nil
}
func Accept(key Key, psk, hello []byte) (*RecordCipher, []byte, error) {
	if len(hello) < 5 || len(hello) > 128 || !bytes.Equal(hello[:5], helloPrefix) {
		return nil, nil, ErrProtocol
	}
	s, e := handshakeState(key, psk, false)
	if e != nil {
		return nil, nil, e
	}
	p, _, _, e := s.ReadMessage(nil, hello[5:])
	if e != nil || !bytes.Equal(p, []byte("client")) {
		return nil, nil, ErrProtocol
	}
	b, c2s, s2c, e := s.WriteMessage(bytes.Clone(acceptPrefix), []byte("server"))
	if e != nil {
		return nil, nil, e
	}
	return newRecordCipher(s2c, c2s, s.ChannelBinding(), false), b, nil
}
func (i *Initiator) Finish(reply []byte) (*RecordCipher, error) {
	if i.finished {
		return nil, ErrState
	}
	if len(reply) < 5 || len(reply) > 128 || !bytes.Equal(reply[:5], acceptPrefix) {
		return nil, ErrProtocol
	}
	i.finished = true
	p, c2s, s2c, e := i.state.ReadMessage(nil, reply[5:])
	if e != nil || !bytes.Equal(p, []byte("server")) {
		return nil, ErrProtocol
	}
	return newRecordCipher(c2s, s2c, i.state.ChannelBinding(), true), nil
}

// ReplayWindow is a receiver-owned bounded bitmap. A far-future authenticated
// sequence is not permission to forget older reliable data. Advance only on an
// authenticated retirement watermark after the sender has proven safe retirement.
type ReplayWindow struct {
	floor uint64
	seen  [ReplaySpan / 64]uint64
}

func (r *ReplayWindow) Check(seq uint64) (duplicate bool, err error) {
	if seq < r.floor {
		return true, nil
	}
	if seq-r.floor >= ReplaySpan {
		return false, ErrCapacity
	}
	return r.seen[(seq%ReplaySpan)/64]&(uint64(1)<<(seq%64)) != 0, nil
}
func (r *ReplayWindow) Commit(seq uint64) error {
	dup, e := r.Check(seq)
	if e != nil || dup {
		return e
	}
	r.seen[(seq%ReplaySpan)/64] |= uint64(1) << (seq % 64)
	return nil
}
func (r *ReplayWindow) Advance(floor uint64) error {
	if floor < r.floor {
		return ErrState
	}
	if floor-r.floor >= ReplaySpan {
		clear(r.seen[:])
	} else {
		for seq := r.floor; seq < floor; seq++ {
			r.seen[(seq%ReplaySpan)/64] &^= uint64(1) << (seq % 64)
		}
	}
	r.floor = floor
	return nil
}
