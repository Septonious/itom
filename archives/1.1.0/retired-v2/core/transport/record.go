// Package transport implements the replacement session transport. It does not
// import the removed transport, scheduler, stream-owner or congestion policies.
// Records here are internal plaintext; a carrier must authenticate and account
// every physical publication before exposing them to Session.
package transport

import (
	"encoding/binary"
	"errors"
)

var ErrProtocol = errors.New("V2_PROTOCOL")
var ErrCapacity = errors.New("V2_CAPACITY")
var ErrClosed = errors.New("V2_CLOSED")

const MaxPayload = 64 << 10
const headerSize = 50
const WireVersion = 2

type Kind uint8

const (
	Data Kind = iota + 1
	Credit
	Finish
	Reset
	Receipt
	FinishAck
	ResetAck
	Open
	OpenOK
	OpenError
	Authority
)

type Record struct {
	Kind                       Kind
	Generation                 uint64
	Stream                     uint64
	Offset                     uint64
	Acknowledged, ReceiveLimit uint64
	Payload                    []byte
}

func (r Record) valid() bool {
	if r.Generation == 0 || r.Stream == 0 && r.Kind != Authority || r.Stream > maxStreamID || len(r.Payload) > MaxPayload || r.Acknowledged > maxStreamID || r.ReceiveLimit > maxStreamID {
		return false
	}
	switch r.Kind {
	case Authority:
		_, _, err := decodeAuthority(r.Payload)
		return err == nil && r.Stream == 0 && r.Offset > 0 && r.Offset <= maxStreamID && r.Acknowledged == 0 && r.ReceiveLimit == 0
	case Open:
		_, err := decodeTarget(r.Payload)
		return err == nil && r.Offset == 0 && r.Acknowledged == 0 && r.ReceiveLimit > 0
	case OpenOK:
		return len(r.Payload) == 0 && r.Offset == 0 && r.Acknowledged == 0 && r.ReceiveLimit > 0
	case OpenError:
		return len(r.Payload) == 1 && r.Payload[0] >= byte(OpenDenied) && r.Payload[0] <= byte(OpenBusy) && r.Offset == 0 && r.Acknowledged == 0 && r.ReceiveLimit == 0
	case Data:
		return len(r.Payload) > 0 && r.Offset <= maxStreamID-uint64(len(r.Payload)) && ((r.ReceiveLimit == 0 && r.Acknowledged == 0) || (r.ReceiveLimit > 0 && r.Acknowledged <= r.ReceiveLimit))
	case Credit:
		return len(r.Payload) == 0 && r.Offset <= maxStreamID && r.ReceiveLimit == 0 && r.Acknowledged <= r.Offset
	case Finish, Receipt, FinishAck:
		return len(r.Payload) == 0 && r.Offset <= maxStreamID && r.Acknowledged == 0 && r.ReceiveLimit == 0
	case Reset, ResetAck:
		return len(r.Payload) == 0 && r.Offset == 0 && r.Acknowledged == 0 && r.ReceiveLimit == 0
	default:
		return false
	}
}
func (r Record) MarshalBinary() ([]byte, error) {
	if !r.valid() {
		return nil, ErrProtocol
	}
	b := make([]byte, headerSize+len(r.Payload))
	b[0] = WireVersion
	b[1] = byte(r.Kind)
	binary.BigEndian.PutUint64(b[2:10], r.Generation)
	binary.BigEndian.PutUint64(b[10:18], r.Stream)
	binary.BigEndian.PutUint64(b[18:26], r.Offset)
	binary.BigEndian.PutUint64(b[26:34], r.Acknowledged)
	binary.BigEndian.PutUint64(b[34:42], r.ReceiveLimit)
	binary.BigEndian.PutUint64(b[42:50], uint64(len(r.Payload)))
	copy(b[50:], r.Payload)
	return b, nil
}
func DecodeRecord(b []byte) (Record, error) {
	if len(b) < headerSize || len(b) > headerSize+MaxPayload || b[0] != WireVersion || binary.BigEndian.Uint64(b[42:50]) != uint64(len(b)-headerSize) {
		return Record{}, ErrProtocol
	}
	r := Record{Kind: Kind(b[1]), Generation: binary.BigEndian.Uint64(b[2:10]), Stream: binary.BigEndian.Uint64(b[10:18]), Offset: binary.BigEndian.Uint64(b[18:26]), Acknowledged: binary.BigEndian.Uint64(b[26:34]), ReceiveLimit: binary.BigEndian.Uint64(b[34:42]), Payload: append([]byte(nil), b[50:]...)}
	if !r.valid() {
		return Record{}, ErrProtocol
	}
	return r, nil
}
