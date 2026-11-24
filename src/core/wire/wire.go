// Package wire defines the bounded, versioned ITOM record envelope.
// Parsing an envelope does not authenticate it. Only the secure package may
// hand a decrypted Frame to a live session.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	Version                = 2
	HeaderSize             = 64
	FrameHeaderSize        = 32
	MaxData                = 32 * 1024
	MaxControl             = 4096
	MaxHandshake           = 4096
	TagSize                = 16
	MaxBody                = HeaderSize + FrameHeaderSize + MaxData + TagSize
	MaxRecord              = HeaderSize + MaxBody
	MaxOffset       uint64 = math.MaxInt64
)

var (
	ErrFormat  = errors.New("WIRE_FORMAT")
	ErrLimit   = errors.New("WIRE_LIMIT")
	ErrVersion = errors.New("WIRE_VERSION")
)

type ID [16]byte
type Direction uint8

const (
	C2S Direction = 0
	S2C Direction = 1
)

func (d Direction) Valid() bool        { return d == C2S || d == S2C }
func (d Direction) Reverse() Direction { return d ^ 1 }

type Kind uint8

const (
	Handshake Kind = 0
	Record    Kind = 1
)

type Header struct {
	Kind       Kind
	Pair       ID
	Session    ID
	Epoch      uint32
	Direction  Direction
	Counter    uint64
	BodyLength uint32
}

func (h Header) Validate() error {
	if (h.Kind != Handshake && h.Kind != Record) || !h.Direction.Valid() ||
		h.Pair == (ID{}) || h.Session == (ID{}) || h.Epoch == 0 ||
		h.Counter == math.MaxUint64 {
		return ErrFormat
	}
	if h.Kind == Handshake {
		if h.BodyLength == 0 || h.BodyLength > MaxHandshake || h.Counter > 1 {
			return ErrLimit
		}
	} else if h.BodyLength < HeaderSize+FrameHeaderSize+TagSize || h.BodyLength > MaxBody {
		return ErrLimit
	}
	return nil
}
func (h Header) MarshalBinary() ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	b := make([]byte, HeaderSize)
	copy(b[:4], "ITM2")
	b[4], b[5] = Version, byte(h.Kind)
	copy(b[8:24], h.Pair[:])
	copy(b[24:40], h.Session[:])
	binary.BigEndian.PutUint32(b[40:44], h.Epoch)
	b[44] = byte(h.Direction)
	binary.BigEndian.PutUint64(b[48:56], h.Counter)
	binary.BigEndian.PutUint32(b[56:60], h.BodyLength)
	return b, nil
}
func ParseHeader(b []byte) (Header, error) {
	var h Header
	if len(b) != HeaderSize {
		return h, ErrFormat
	}
	if string(b[:4]) != "ITM2" || b[4] != Version {
		return h, ErrVersion
	}
	for _, i := range []int{6, 7, 45, 46, 47, 60, 61, 62, 63} {
		if b[i] != 0 {
			return h, ErrFormat
		}
	}
	h.Kind, h.Direction = Kind(b[5]), Direction(b[44])
	copy(h.Pair[:], b[8:24])
	copy(h.Session[:], b[24:40])
	h.Epoch = binary.BigEndian.Uint32(b[40:44])
	h.Counter = binary.BigEndian.Uint64(b[48:56])
	h.BodyLength = binary.BigEndian.Uint32(b[56:60])
	return h, h.Validate()
}

// ReadEnvelope reads precisely one record and bounds allocation BEFORE reading
// its body. A truncated record is not recoverable by scanning for magic.
func ReadEnvelope(r io.Reader) (Header, []byte, error) {
	b := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, b); err != nil {
		return Header{}, nil, err
	}
	h, err := ParseHeader(b)
	if err != nil {
		return Header{}, nil, err
	}
	body := make([]byte, int(h.BodyLength))
	if _, err = io.ReadFull(r, body); err != nil {
		return Header{}, nil, err
	}
	return h, body, nil
}

type Type uint8

const (
	Confirm                Type = 1
	Ready                  Type = 2
	ReadyAck               Type = 3
	CoordinatorInfo        Type = 4
	CoordinatorAck         Type = 5
	MailReceipt            Type = 6
	ServerMessage          Type = 7
	ServerMessageAck       Type = 8
	Open                   Type = 16
	OpenOK                 Type = 17
	OpenError              Type = 18
	Data                   Type = 32
	Receipt                Type = 33
	Window                 Type = 34
	Status                 Type = 35
	StatusRequest          Type = 36
	Nack                   Type = 37
	Fin                    Type = 48
	FinAck                 Type = 49
	Reset                  Type = 50
	ResetAck               Type = 51
	Ping                   Type = 64
	Pong                   Type = 65
	SessionClose           Type = 66
	SessionError           Type = 67
	RekeyRequest           Type = 80
	RekeyInit              Type = 81
	RekeyResponse          Type = 82
	WriteGrant             Type = 96
	GrantUsage             Type = 97
	GrantRelease           Type = 98
	GrantReconcile         Type = 99
	RecoveryFastWriteGrant Type = 100
	GCStatus               Type = 112
	GCProofRequest         Type = 113
	GrantRetire            Type = 114
	GrantRetireAck         Type = 115
)

func (t Type) String() string {
	switch t {
	case Confirm:
		return "CONFIRM"
	case Ready:
		return "READY"
	case ReadyAck:
		return "READY_ACK"
	case MailReceipt:
		return "MAIL_RECEIPT"
	case ServerMessage:
		return "SERVER_MESSAGE"
	case ServerMessageAck:
		return "SERVER_MESSAGE_ACK"
	case CoordinatorInfo:
		return "COORDINATOR_INFO"
	case CoordinatorAck:
		return "COORDINATOR_ACK"
	case Open:
		return "OPEN"
	case OpenOK:
		return "OPEN_OK"
	case OpenError:
		return "OPEN_ERROR"
	case Data:
		return "DATA"
	case Receipt:
		return "RECEIPT"
	case Window:
		return "WINDOW"
	case Status:
		return "STATUS"
	case StatusRequest:
		return "STATUS_REQUEST"
	case Nack:
		return "NACK"
	case Fin:
		return "FIN"
	case FinAck:
		return "FIN_ACK"
	case Reset:
		return "RESET"
	case ResetAck:
		return "RESET_ACK"
	case Ping:
		return "PING"
	case Pong:
		return "PONG"
	case SessionClose:
		return "SESSION_CLOSE"
	case SessionError:
		return "SESSION_ERROR"
	case RekeyRequest:
		return "REKEY_REQUEST"
	case RekeyInit:
		return "REKEY_INIT"
	case RekeyResponse:
		return "REKEY_RESPONSE"
	case WriteGrant:
		return "WRITE_GRANT"
	case RecoveryFastWriteGrant:
		return "RECOVERY_FAST_WRITE_GRANT"
	case GrantReconcile:
		return "GRANT_RECONCILE"
	case GrantUsage:
		return "GRANT_USAGE"
	case GrantRelease:
		return "GRANT_RELEASE"
	case GrantRetire:
		return "GRANT_RETIRE"
	case GrantRetireAck:
		return "GRANT_RETIRE_ACK"
	case GCStatus:
		return "GC_STATUS"
	case GCProofRequest:
		return "GC_PROOF_REQUEST"
	default:
		return "UNKNOWN"
	}
}
func (t Type) streamScoped() bool {
	switch t {
	case Open, OpenOK, OpenError, Data, Receipt, Window, Status, StatusRequest, Nack, Fin, FinAck, Reset, ResetAck:
		return true
	}
	return false
}

type Frame struct {
	Type    Type
	Stream  uint64
	Logical uint64
	Offset  uint64
	Payload []byte
}

func (f Frame) End() (uint64, error) {
	if f.Offset > MaxOffset || uint64(len(f.Payload)) > MaxOffset-f.Offset {
		return 0, ErrLimit
	}
	return f.Offset + uint64(len(f.Payload)), nil
}
func (f Frame) Validate() error {
	if f.Type.String() == "UNKNOWN" || f.Offset > MaxOffset {
		return ErrFormat
	}
	if f.Type.streamScoped() {
		if f.Stream == 0 || f.Stream%2 != 1 || f.Stream > MaxOffset {
			return ErrFormat
		}
	} else if f.Stream != 0 || f.Offset != 0 {
		return ErrFormat
	}
	if f.Type == Data {
		if f.Logical == 0 || len(f.Payload) == 0 || len(f.Payload) > MaxData {
			return ErrLimit
		}
		_, err := f.End()
		return err
	}
	if f.Logical != 0 || len(f.Payload) > MaxControl {
		return ErrLimit
	}
	return nil
}
func (f Frame) MarshalBinary() ([]byte, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	b := make([]byte, FrameHeaderSize+len(f.Payload))
	b[0] = byte(f.Type)
	binary.BigEndian.PutUint64(b[4:12], f.Stream)
	binary.BigEndian.PutUint64(b[12:20], f.Logical)
	binary.BigEndian.PutUint64(b[20:28], f.Offset)
	binary.BigEndian.PutUint32(b[28:32], uint32(len(f.Payload)))
	copy(b[32:], f.Payload)
	return b, nil
}
func ParseFrame(b []byte) (Frame, error) {
	var f Frame
	if len(b) < FrameHeaderSize || len(b) > FrameHeaderSize+MaxData ||
		b[1] != 0 || b[2] != 0 || b[3] != 0 {
		return f, ErrFormat
	}
	n := binary.BigEndian.Uint32(b[28:32])
	if uint64(n) != uint64(len(b)-FrameHeaderSize) {
		return f, ErrLimit
	}
	f = Frame{Type: Type(b[0]), Stream: binary.BigEndian.Uint64(b[4:12]),
		Logical: binary.BigEndian.Uint64(b[12:20]), Offset: binary.BigEndian.Uint64(b[20:28])}
	// Validate the payload size BEFORE copying. The input is already bounded.
	f.Payload = b[32:]
	if err := f.Validate(); err != nil {
		return Frame{}, err
	}
	f.Payload = append([]byte(nil), f.Payload...)
	return f, nil
}
func Uint64(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}
func ParseUint64(b []byte) (uint64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("%w: uint64 length", ErrFormat)
	}
	return binary.BigEndian.Uint64(b), nil
}
