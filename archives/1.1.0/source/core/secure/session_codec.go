package secure

import (
	"bytes"
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
	if !e.sessionV2 {
		return nil, ErrContext
	}
	if len(packet) < sessionHeader+wire.TagSize+1 || len(packet) > sessionHeader+wire.TagSize+SessionMaxPlaintext {
		return nil, wire.ErrLimit
	}
	h := packet[:sessionHeader]
	if string(h[:4]) != "ITS2" || !bytes.Equal(h[4:20], e.binding.Pair[:]) || !bytes.Equal(h[20:36], e.binding.Session[:]) || binary.BigEndian.Uint32(h[36:40]) != e.binding.Epoch || h[40] != byte(e.direction.Reverse()) || int(binary.BigEndian.Uint32(h[56:60])) != len(packet)-sessionHeader {
		return nil, ErrContext
	}
	for _, i := range []int{41, 42, 43, 44, 45, 46, 47, 60, 61, 62, 63} {
		if h[i] != 0 {
			return nil, ErrContext
		}
	}
	counter := binary.BigEndian.Uint64(h[48:56])
	if counter >= HardRecords {
		return nil, ErrKeyLimit
	}
	e.rxMu.Lock()
	defer e.rxMu.Unlock()
	if e.rxFailures >= 1024 {
		return nil, ErrKeyLimit
	}
	if err := e.replay.check(counter); err != nil {
		return nil, err
	}
	e.rx.SetNonce(counter)
	plain, err := e.rx.Decrypt(nil, h, packet[sessionHeader:])
	if err != nil {
		e.rxFailures++
		return nil, ErrAuth
	}
	n := uint64(len(plain) + sessionHeader)
	if e.rxBytes > HardBytes || n > HardBytes-e.rxBytes {
		clear(plain)
		return nil, ErrKeyLimit
	}
	e.rxBytes += n
	e.replay.commit(counter)
	return plain, nil
}

// SessionPacketSize is the exact stored packet length before encryption; it
// allocates no key state or nonce and conveys no publication permission.
func SessionPacketSize(plaintextBytes int) (int, error) {
	if plaintextBytes < 1 || plaintextBytes > SessionMaxPlaintext {
		return 0, wire.ErrLimit
	}
	return sessionHeader + plaintextBytes + wire.TagSize, nil
}
