package transport

import (
	"bytes"
	"encoding/binary"
	"itom.local/core/secure"
)

const bulkMagic = "ISB2"
const bulkHeader = 8
const bulkEntryHeader = 4
const MaxBulkRecords = secure.SessionBatchMaxPackets
const MaxBulkBytes = secure.SessionBatchMaxBytes

// Bulk containers group independent, authenticated DATA records under one
// physical ledger entry. Their framing is additionally covered by the physical
// envelope MAC. They cannot contain control or cross-generation records.
func (c *Cipher) SealRecords(records []Record) ([]byte, error) {
	if len(records) < 1 || len(records) > MaxBulkRecords {
		return nil, ErrCapacity
	}
	total := bulkHeader
	generation := records[0].Generation
	for _, r := range records {
		if !r.valid() || r.Kind != Data || r.Generation != generation {
			return nil, ErrProtocol
		}
		size, err := secure.SessionPacketSize(headerSize + len(r.Payload))
		if err != nil {
			return nil, err
		}
		if size+bulkEntryHeader > MaxBulkBytes-total {
			return nil, ErrCapacity
		}
		total += size + bulkEntryHeader
	}
	body := make([]byte, bulkHeader, total)
	copy(body, bulkMagic)
	binary.BigEndian.PutUint16(body[4:6], uint16(len(records)))
	for _, r := range records {
		packet, err := c.Seal(r)
		if err != nil {
			clear(body)
			return nil, err
		}
		body = binary.BigEndian.AppendUint32(body, uint32(len(packet)))
		body = append(body, packet...)
		clear(packet)
	}
	return body, nil
}
func parseBulk(body []byte) ([][]byte, error) {
	if len(body) < bulkHeader || len(body) > MaxBulkBytes || !bytes.HasPrefix(body, []byte(bulkMagic)) || body[6] != 0 || body[7] != 0 {
		return nil, ErrProtocol
	}
	count := int(binary.BigEndian.Uint16(body[4:6]))
	if count < 1 || count > MaxBulkRecords {
		return nil, ErrProtocol
	}
	remaining := body[bulkHeader:]
	packets := make([][]byte, 0, count)
	maximum, _ := secure.SessionPacketSize(secure.SessionMaxPlaintext)
	for range count {
		if len(remaining) < bulkEntryHeader {
			return nil, ErrProtocol
		}
		n := uint64(binary.BigEndian.Uint32(remaining[:bulkEntryHeader]))
		remaining = remaining[bulkEntryHeader:]
		if n == 0 || n > uint64(maximum) || n > uint64(len(remaining)) {
			return nil, ErrProtocol
		}
		packets = append(packets, remaining[:int(n)])
		remaining = remaining[int(n):]
	}
	if len(remaining) != 0 {
		return nil, ErrProtocol
	}
	return packets, nil
}
func (c *Cipher) OpenRecords(body []byte) ([]Record, error) { return c.OpenRecordsFor(body, AnyLedger) }
func (c *Cipher) OpenRecordsFor(body []byte, class LedgerClass) ([]Record, error) {
	return c.openRecordsFor(body, class, 0)
}
func (c *Cipher) openRecordsFor(body []byte, class LedgerClass, expectedGeneration uint64) (records []Record, err error) {
	if class > ControlLedger {
		return nil, ErrProtocol
	}
	bundled := bytes.HasPrefix(body, []byte(bulkMagic))
	packets := [][]byte{body}
	if bundled {
		if class == ControlLedger {
			return nil, ErrProtocol
		}
		packets, err = parseBulk(body)
		if err != nil {
			return nil, err
		}
	}
	plains, err := c.epoch.OpenSessionBatch(packets, func(plains [][]byte) error {
		var generation uint64
		for _, plain := range plains {
			r, e := DecodeRecord(plain)
			if e != nil {
				return e
			}
			if expectedGeneration != 0 && r.Generation != expectedGeneration || !class.accepts(r.Kind) || bundled && (r.Kind != Data || generation != 0 && r.Generation != generation) {
				clear(r.Payload)
				return ErrProtocol
			}
			generation = r.Generation
			records = append(records, r)
		}
		return nil
	})
	if err != nil {
		for _, r := range records {
			clear(r.Payload)
		}
		return nil, err
	}
	for _, plain := range plains {
		clear(plain)
	}
	return records, nil
}
