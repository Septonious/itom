package secure

import (
	"bytes"
	"encoding/binary"
	"itom.local/core/wire"
)

const SessionBatchMaxPackets = wire.MaxDataBatchRecords
const SessionBatchMaxBytes = wire.MaxDataBatchBody

func (e *Epoch) sessionPacketHeader(packet []byte) ([]byte, uint64, error) {
	if !e.sessionV2 {
		return nil, 0, ErrContext
	}
	if len(packet) < sessionHeader+wire.TagSize+1 || len(packet) > sessionHeader+wire.TagSize+SessionMaxPlaintext {
		return nil, 0, wire.ErrLimit
	}
	h := packet[:sessionHeader]
	if string(h[:4]) != "ITS2" || !bytes.Equal(h[4:20], e.binding.Pair[:]) || !bytes.Equal(h[20:36], e.binding.Session[:]) || binary.BigEndian.Uint32(h[36:40]) != e.binding.Epoch || h[40] != byte(e.direction.Reverse()) || int(binary.BigEndian.Uint32(h[56:60])) != len(packet)-sessionHeader {
		return nil, 0, ErrContext
	}
	for _, i := range []int{41, 42, 43, 44, 45, 46, 47, 60, 61, 62, 63} {
		if h[i] != 0 {
			return nil, 0, ErrContext
		}
	}
	counter := binary.BigEndian.Uint64(h[48:56])
	if counter >= HardRecords {
		return nil, 0, ErrKeyLimit
	}
	return h, counter, nil
}

// OpenSessionBatch verifies an entire bounded publication before committing
// any replay counters. validate must be a pure, nonblocking plaintext validator;
// transport uses it to reject malformed records and mixed ledger classes.
// Successful decryptions still count against the original key-usage ceiling
// when a later record/validator fails. No plaintext escapes on failure.
func (e *Epoch) OpenSessionBatch(packets [][]byte, validate func([][]byte) error) (plains [][]byte, err error) {
	if !e.sessionV2 {
		return nil, ErrContext
	}
	if len(packets) < 1 || len(packets) > SessionBatchMaxPackets {
		return nil, wire.ErrLimit
	}
	total := 0
	for _, p := range packets {
		if len(p) > SessionBatchMaxBytes-total {
			return nil, wire.ErrLimit
		}
		total += len(p)
	}
	e.rxMu.Lock()
	defer e.rxMu.Unlock()
	if e.rxFailures >= 1024 {
		return nil, ErrKeyLimit
	}
	var staged *replayWindow
	if len(packets) > 1 {
		copy := e.replay
		staged = &copy
	}
	view := &e.replay
	if staged != nil {
		view = staged
	}
	var singleCounter uint64
	defer func() {
		if err != nil {
			for _, plain := range plains {
				clear(plain)
			}
			plains = nil
		}
	}()
	for _, packet := range packets {
		header, counter, check := e.sessionPacketHeader(packet)
		if check != nil {
			return plains, check
		}
		if check = view.check(counter); check != nil {
			return plains, check
		}
		e.rx.SetNonce(counter)
		plain, check := e.rx.Decrypt(nil, header, packet[sessionHeader:])
		if check != nil {
			e.rxFailures++
			return plains, ErrAuth
		}
		n := uint64(len(plain) + sessionHeader)
		if e.rxBytes > HardBytes || n > HardBytes-e.rxBytes {
			clear(plain)
			return plains, ErrKeyLimit
		}
		e.rxBytes += n
		plains = append(plains, plain)
		if staged != nil {
			staged.commit(counter)
		} else {
			singleCounter = counter
		}
	}
	if validate != nil {
		if err = validate(plains); err != nil {
			return plains, err
		}
	}
	if staged != nil {
		e.replay = *staged
	} else {
		e.replay.commit(singleCounter)
	}
	return plains, nil
}
