// Package v160 owns the new transport contracts. Mailbox routing identifiers
// are not secrets or authentication credentials.
package v160

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

var (
	ErrProtocol = errors.New("v160: protocol violation")
	ErrCapacity = errors.New("v160: capacity exhausted")
	ErrConflict = errors.New("v160: identity or generation conflict")
	ErrState    = errors.New("v160: invalid lifecycle transition")
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
const slots = base62 + "-_"

type UserID string
type SessionID uint32
type Key struct {
	User    UserID
	Session SessionID
}

func (u UserID) Valid() bool {
	if len(u) != 4 {
		return false
	}
	for _, c := range []byte(u) {
		if strings.IndexByte(base62, c) < 0 {
			return false
		}
	}
	return true
}
func (k Key) Valid() bool { return k.User.Valid() && k.Session != 0 }

// RandomUser uses rejection sampling; all Base62 strings are equiprobable.
func RandomUser() (UserID, error) {
	var out [4]byte
	var b [16]byte
	for i := 0; i < len(out); {
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		for _, v := range b {
			if v >= 248 {
				continue
			}
			out[i] = base62[int(v)%62]
			i++
			if i == len(out) {
				break
			}
		}
	}
	return UserID(out[:]), nil
}
func RandomSession() (SessionID, error) {
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		if n := binary.BigEndian.Uint32(b[:]); n != 0 {
			return SessionID(n), nil
		}
	}
}
func (s SessionID) String() string {
	n := uint32(s)
	var b [6]byte
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = base62[n%62]
		n /= 62
	}
	return string(b[:])
}
func ParseSession(s string) (SessionID, error) {
	if len(s) != 6 {
		return 0, ErrProtocol
	}
	var n uint64
	for i := range len(s) {
		d := strings.IndexByte(base62, s[i])
		if d < 0 {
			return 0, ErrProtocol
		}
		n = n*62 + uint64(d)
	}
	if n == 0 || n > math.MaxUint32 {
		return 0, ErrProtocol
	}
	return SessionID(n), nil
}

type Traffic uint8

const (
	DataUp Traffic = iota + 1
	DataDown
	ControlUp
	ControlDown
)

func (t Traffic) String() string {
	switch t {
	case DataUp:
		return "DU"
	case DataDown:
		return "DD"
	case ControlUp:
		return "CU"
	case ControlDown:
		return "CD"
	}
	return ""
}
func (t Traffic) Capacity() int {
	switch t {
	case DataUp, DataDown:
		return 256
	case ControlUp, ControlDown:
		return 512
	}
	return 0
}
func (t Traffic) OrdinaryLimit() int {
	if t.Capacity() == 512 {
		return 384
	}
	return t.Capacity()
}

type Folder struct {
	Key  Key
	Type Traffic
	Slot uint8
}

func (f Folder) Valid() bool { return f.Key.Valid() && f.Type.Capacity() > 0 && f.Slot < 64 }
func (f Folder) Name() (string, error) {
	if !f.Valid() {
		return "", ErrProtocol
	}
	return string(f.Key.User) + "-" + f.Type.String() + "-" + f.Key.Session.String() + "-" + string(slots[f.Slot]), nil
}
func ParseFolder(s string) (Folder, error) {
	if len(s) != 16 || s[4] != '-' || s[7] != '-' || s[14] != '-' {
		return Folder{}, ErrProtocol
	}
	u := UserID(s[:4])
	id, err := ParseSession(s[8:14])
	if err != nil || !u.Valid() {
		return Folder{}, ErrProtocol
	}
	var t Traffic
	switch s[5:7] {
	case "DU":
		t = DataUp
	case "DD":
		t = DataDown
	case "CU":
		t = ControlUp
	case "CD":
		t = ControlDown
	default:
		return Folder{}, ErrProtocol
	}
	i := strings.IndexByte(slots, s[15])
	if i < 0 {
		return Folder{}, ErrProtocol
	}
	return Folder{Key{u, id}, t, uint8(i)}, nil
}
