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
const headerSize = 34
const WireVersion = 2

type Kind uint8

const (
	Data Kind = iota + 1
	Credit
	Finish
	Reset
)

type Record struct {
	Kind       Kind
	Generation uint64
	Stream     uint64
	Offset     uint64
	Payload    []byte
}

func (r Record) valid() bool {
	if r.Generation == 0 || r.Stream == 0 || len(r.Payload) > MaxPayload {
		return false
	}
	switch r.Kind {
	case Data:
		return len(r.Payload) > 0 && r.Offset <= ^uint64(0)-uint64(len(r.Payload))
	case Credit, Finish, Reset:
		return len(r.Payload) == 0
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
	binary.BigEndian.PutUint64(b[26:34], uint64(len(r.Payload)))
	copy(b[34:], r.Payload)
	return b, nil
}
func DecodeRecord(b []byte) (Record, error) {
	if len(b) < headerSize || len(b) > headerSize+MaxPayload || b[0] != WireVersion || binary.BigEndian.Uint64(b[26:34]) != uint64(len(b)-headerSize) {
		return Record{}, ErrProtocol
	}
	r := Record{Kind: Kind(b[1]), Generation: binary.BigEndian.Uint64(b[2:10]), Stream: binary.BigEndian.Uint64(b[10:18]), Offset: binary.BigEndian.Uint64(b[18:26]), Payload: append([]byte(nil), b[34:]...)}
	if !r.valid() {
		return Record{}, ErrProtocol
	}
	return r, nil
}
