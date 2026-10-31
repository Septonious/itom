package transport

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func FuzzBulkContainerFramingIsCanonicalAndBounded(f *testing.F) {
	seed := []byte{'I', 'S', 'B', '2', 0, 1, 0, 0, 0, 0, 0, 1, 0}
	f.Add(seed)
	f.Add([]byte("ISB2\xff\xff\x00\x00"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, body []byte) {
		packets, err := parseBulk(body)
		if err != nil {
			return
		}
		if len(packets) < 1 || len(packets) > MaxBulkRecords || len(body) > MaxBulkBytes {
			t.Fatal("unbounded container")
		}
		encoded := make([]byte, 8)
		copy(encoded, bulkMagic)
		binary.BigEndian.PutUint16(encoded[4:6], uint16(len(packets)))
		for _, p := range packets {
			encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(p)))
			encoded = append(encoded, p...)
		}
		if !bytes.Equal(body, encoded) {
			t.Fatal("noncanonical frame accepted")
		}
	})
}
