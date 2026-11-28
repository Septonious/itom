package v160

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"sync"
)

const ReliableRecords = 2048
const ReliableBytes = 32 << 20

// Receipt ranges encode (start,count) using canonical varints. A range never
// includes an unassigned sequence; the sender validates this independently.
func EncodeReceipts(ids []uint64) ([]byte, error) {
	if len(ids) == 0 || len(ids) > ReplaySpan {
		return nil, ErrCapacity
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var b []byte
	for i := 0; i < len(ids); {
		if ids[i] == math.MaxUint64 {
			return nil, ErrProtocol
		}
		j := i + 1
		for j < len(ids) && ids[j] != math.MaxUint64 && ids[j] == ids[j-1]+1 {
			j++
		}
		b = binary.AppendUvarint(b, ids[i])
		b = binary.AppendUvarint(b, uint64(j-i))
		i = j
	}
	if len(b) > 8192 {
		return nil, ErrCapacity
	}
	return b, nil
}
func DecodeReceipts(b []byte) ([]uint64, error) {
	if len(b) == 0 || len(b) > 8192 {
		return nil, ErrProtocol
	}
	var ids []uint64
	var last uint64
	for len(b) > 0 {
		start, e := readVar(&b)
		if e != nil {
			return nil, e
		}
		count, e := readVar(&b)
		if e != nil {
			return nil, e
		}
		if count == 0 || count > uint64(ReplaySpan-len(ids)) || start > math.MaxUint64-count || len(ids) > 0 && start <= last+1 {
			return nil, ErrProtocol
		}
		for i := uint64(0); i < count; i++ {
			ids = append(ids, start+i)
		}
		last = start + count - 1
	}
	return ids, nil
}

// ReliableChannel owns one directional cipher's entire send sequence space.
// Reliable records retain immutable ciphertext until authenticated receipts.
// Receipt-only records are idempotent and may use the same nonce allocator
// beyond the reliable span; they can therefore never be blocked by a missing
// reverse DATA record. The authenticated sender floor fences old duplicates.
// Receive has one owner; send operations and receipt application are serialized
// by sendMu. The apply callback must atomically accept all frames in a record,
// or return ErrCapacity without any state mutation.
type ReliableChannel struct {
	cipher       *RecordCipher
	sendMu       sync.Mutex
	pending      map[uint64][]byte
	pendingBytes int
	recv         ReplayWindow
}

func NewReliableChannel(c *RecordCipher) *ReliableChannel {
	return &ReliableChannel{cipher: c, pending: make(map[uint64][]byte)}
}
func (c *ReliableChannel) floorLocked() uint64 {
	floor := c.cipher.nextSequence()
	for seq := range c.pending {
		floor = min(floor, seq)
	}
	return floor
}
func (c *ReliableChannel) Send(frames []Frame) ([]byte, uint64, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if len(frames) == 0 || len(frames) >= MaxFrames || len(c.pending) >= ReliableRecords {
		return nil, 0, ErrCapacity
	}
	for _, f := range frames {
		if f.Kind == Receipt || f.Kind == SequenceFloor {
			return nil, 0, ErrProtocol
		}
	}
	floor := c.floorLocked()
	if e := c.cipher.AdvanceSendFloor(floor); e != nil {
		return nil, 0, e
	}
	all := make([]Frame, 0, len(frames)+1)
	all = append(all, Frame{Kind: SequenceFloor, Offset: floor})
	all = append(all, frames...)
	plain, e := EncodeFrames(all)
	if e != nil {
		return nil, 0, e
	}
	if len(plain)+RecordOverhead > ReliableBytes-c.pendingBytes {
		return nil, 0, ErrCapacity
	}
	w, seq, e := c.cipher.sealPlain(plain, false)
	if e != nil {
		return nil, 0, e
	}
	c.pending[seq] = w
	c.pendingBytes += len(w)
	return bytes.Clone(w), seq, nil
}
func (c *ReliableChannel) Feedback(ids []uint64) ([]byte, uint64, error) {
	b, e := EncodeReceipts(ids)
	if e != nil {
		return nil, 0, e
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	floor := c.floorLocked()
	if e = c.cipher.AdvanceSendFloor(floor); e != nil {
		return nil, 0, e
	}
	p, e := EncodeFrames([]Frame{{Kind: SequenceFloor, Offset: floor}, {Kind: Receipt, Payload: b}})
	if e != nil {
		return nil, 0, e
	}
	// These records have no non-idempotent effects and are never acknowledged.
	return c.cipher.sealPlain(p, true)
}
func (c *ReliableChannel) Repair(seq uint64) ([]byte, bool) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	w, ok := c.pending[seq]
	return bytes.Clone(w), ok
}
func (c *ReliableChannel) Pending() (int, int) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return len(c.pending), c.pendingBytes
}

type ReceiveResult struct {
	Sequence                uint64
	NeedsReceipt, Duplicate bool
	Acknowledged            []uint64
	AcknowledgedWireBytes   int
}

func (c *ReliableChannel) Receive(wire []byte, apply func([]Frame) error) (ReceiveResult, error) {
	fs, seq, e := c.cipher.Open(wire)
	if e != nil {
		return ReceiveResult{}, e
	}
	if len(fs) < 2 || fs[0].Kind != SequenceFloor || fs[0].Offset > seq {
		return ReceiveResult{}, ErrProtocol
	}
	floor := fs[0].Offset
	fs = fs[1:]
	res := ReceiveResult{Sequence: seq}
	if fs[0].Kind == Receipt {
		if len(fs) != 1 {
			return ReceiveResult{}, ErrProtocol
		}
		ids, e := DecodeReceipts(fs[0].Payload)
		if e != nil {
			return ReceiveResult{}, e
		}
		c.sendMu.Lock()
		defer c.sendMu.Unlock()
		next := c.cipher.nextSequence()
		// Validate the whole receipt before freeing even one record.
		if ids[len(ids)-1] >= next {
			return ReceiveResult{}, ErrProtocol
		}
		if floor > c.recv.floor {
			if e = c.recv.Advance(floor); e != nil {
				return ReceiveResult{}, e
			}
		}
		for _, id := range ids {
			if w, ok := c.pending[id]; ok {
				delete(c.pending, id)
				c.pendingBytes -= len(w)
				res.Acknowledged = append(res.Acknowledged, id)
				res.AcknowledgedWireBytes += len(w)
			}
		}
		if e = c.cipher.AdvanceSendFloor(c.floorLocked()); e != nil {
			return ReceiveResult{}, e
		}
		return res, nil
	}
	for _, f := range fs {
		if f.Kind == Receipt || f.Kind == SequenceFloor {
			return ReceiveResult{}, ErrProtocol
		}
	}
	if floor > c.recv.floor {
		if e = c.recv.Advance(floor); e != nil {
			return ReceiveResult{}, e
		}
	}
	dup, e := c.recv.Check(seq)
	if e != nil {
		return ReceiveResult{}, e
	}
	res.NeedsReceipt = true
	res.Duplicate = dup
	if dup {
		return res, nil
	}
	if apply == nil {
		return ReceiveResult{}, ErrState
	}
	if e = apply(fs); e != nil {
		return ReceiveResult{}, e
	}
	if e = c.recv.Commit(seq); e != nil {
		return ReceiveResult{}, e
	}
	return res, nil
}
