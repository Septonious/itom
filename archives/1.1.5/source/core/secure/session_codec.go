package secure

import (
	"encoding/binary"
	"itom.local/core/wire"
)

const SessionMaxPlaintext = (64 << 10) + 50
const sessionHeader = 64

// SealSession authenticates a v2 record. Keys and nonce allocation stay inside
// Epoch; callers cannot reuse a nonce or bypass the existing key usage limits.
// interactive headroom is reserved for protocol progress, never bulk inventory.
func (e *Epoch) SealSession(plain []byte, interactive bool) ([]byte, error) {
	if !e.sessionV2 {
		return nil, ErrContext
	}
	if len(plain) == 0 || len(plain) > SessionMaxPlaintext {
		return nil, wire.ErrLimit
	}
	e.txMu.Lock()
	defer e.txMu.Unlock()
	n := uint64(len(plain) + sessionHeader)
	if e.tx.Nonce() >= HardRecords || e.txBytes > HardBytes || n > HardBytes-e.txBytes {
		return nil, ErrKeyLimit
	}
	if !interactive && (e.tx.Nonce() >= HardRecords-64 || e.txBytes+n > HardBytes-ControlHeadroom) {
		return nil, ErrKeyLimit
	}
	header := make([]byte, sessionHeader)
	copy(header, "ITS2")
	copy(header[4:20], e.binding.Pair[:])
	copy(header[20:36], e.binding.Session[:])
	binary.BigEndian.PutUint32(header[36:40], e.binding.Epoch)
	header[40] = byte(e.direction)
	binary.BigEndian.PutUint64(header[48:56], e.tx.Nonce())
	binary.BigEndian.PutUint32(header[56:60], uint32(len(plain)+wire.TagSize))
	encrypted, err := e.tx.Encrypt(nil, header, plain)
	if err != nil {
		return nil, ErrKeyLimit
	}
	e.txBytes += n
	return append(header, encrypted...), nil
}

// OpenSession authenticates before consuming a replay slot. Own-direction
// mailbox loopback and every other session/account epoch are rejected here.
func (e *Epoch) OpenSession(packet []byte) ([]byte, error) {
	plains, err := e.OpenSessionBatch([][]byte{packet}, nil)
	if err != nil {
		return nil, err
	}
	return plains[0], nil
}

// SessionPacketSize is the exact stored packet length before encryption; it
// allocates no key state or nonce and conveys no publication permission.
func SessionPacketSize(plaintextBytes int) (int, error) {
	if plaintextBytes < 1 || plaintextBytes > SessionMaxPlaintext {
		return 0, wire.ErrLimit
	}
	return sessionHeader + plaintextBytes + wire.TagSize, nil
}
