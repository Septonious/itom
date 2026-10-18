package wire

import (
	"bytes"
	"encoding/binary"
)

const (
	DataBatchMagic       = "ITD3"
	MaxDataBatchRecords  = 16
	MaxDataBatchPayload  = 576 << 10
	dataBatchHeaderBytes = 24
	// MaxDataBatchBody is the largest decoded ITD3 container before the
	// optional ITQ1 physical binding. It is exported so mailbox admission can
	// reject oversized physical candidates without falling back to the old
	// single-record MaxRecord limit.
	MaxDataBatchBody = dataBatchHeaderBytes + MaxDataBatchRecords*4 + MaxDataBatchPayload
)

// PackDataBatch groups independently authenticated Noise records into one
// physical mail payload. The outer container is only framing: every member is
// still authenticated and replay-checked by secure.Epoch.Open before delivery.
// Batch is a random logical identifier used for diagnostics/correlation and is
// intentionally not a replacement for the physical journal attempt ID.
func PackDataBatch(batch ID, packets [][]byte) ([]byte, error) {
	if batch == (ID{}) || len(packets) == 0 || len(packets) > MaxDataBatchRecords {
		return nil, ErrLimit
	}
	var first Header
	payload := 0
	for i, packet := range packets {
		if len(packet) < HeaderSize || len(packet) > MaxRecord {
			return nil, ErrLimit
		}
		h, err := ParseHeader(packet[:HeaderSize])
		if err != nil || h.Kind != Record || int(h.BodyLength) != len(packet)-HeaderSize {
			return nil, ErrFormat
		}
		if i == 0 {
			first = h
		} else if h.Pair != first.Pair || h.Session != first.Session || h.Epoch != first.Epoch || h.Direction != first.Direction || h.Counter <= first.Counter {
			return nil, ErrFormat
		} else {
			first.Counter = h.Counter
		}
		payload += len(packet)
		if payload > MaxDataBatchPayload {
			return nil, ErrLimit
		}
	}
	out := make([]byte, 0, dataBatchHeaderBytes+len(packets)*4+payload)
	out = append(out, DataBatchMagic...)
	out = append(out, batch[:]...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(packets)))
	out = append(out, 0, 0)
	for _, packet := range packets {
		out = binary.BigEndian.AppendUint32(out, uint32(len(packet)))
		out = append(out, packet...)
	}
	return out, nil
}

// UnpackDataBatch validates all framing and encrypted record envelopes before
// any Noise replay state is consumed. Returned packet slices alias body and are
// valid only while body is retained by the caller.
func UnpackDataBatch(body []byte) (ID, [][]byte, error) {
	var batch ID
	if len(body) < dataBatchHeaderBytes || !bytes.Equal(body[:4], []byte(DataBatchMagic)) {
		return batch, nil, ErrFormat
	}
	copy(batch[:], body[4:20])
	count := int(binary.BigEndian.Uint16(body[20:22]))
	if batch == (ID{}) || count < 1 || count > MaxDataBatchRecords || body[22] != 0 || body[23] != 0 {
		return ID{}, nil, ErrFormat
	}
	body = body[dataBatchHeaderBytes:]
	packets := make([][]byte, 0, count)
	var first Header
	payload := 0
	for i := 0; i < count; i++ {
		if len(body) < 4 {
			return ID{}, nil, ErrFormat
		}
		n := int(binary.BigEndian.Uint32(body[:4]))
		body = body[4:]
		if n < HeaderSize || n > MaxRecord || n > len(body) {
			return ID{}, nil, ErrLimit
		}
		packet := body[:n]
		h, err := ParseHeader(packet[:HeaderSize])
		if err != nil || h.Kind != Record || int(h.BodyLength) != n-HeaderSize {
			return ID{}, nil, ErrFormat
		}
		if i == 0 {
			first = h
		} else if h.Pair != first.Pair || h.Session != first.Session || h.Epoch != first.Epoch || h.Direction != first.Direction || h.Counter <= first.Counter {
			return ID{}, nil, ErrFormat
		} else {
			first.Counter = h.Counter
		}
		payload += n
		if payload > MaxDataBatchPayload {
			return ID{}, nil, ErrLimit
		}
		packets = append(packets, packet)
		body = body[n:]
	}
	if len(body) != 0 {
		return ID{}, nil, ErrFormat
	}
	return batch, packets, nil
}
