package tunnel

import (
	"bytes"
	"testing"

	"itom.local/core/wire"
)

func TestRev4MetadataSizingMatchesPayloadWithoutMutation(t *testing.T) {
	l := &lane{side: true}
	for _, n := range []int{1, 57, 4096, wire.MaxData} {
		payload := bytes.Repeat([]byte{0xa5}, n)
		f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: payload}
		want, err := l.Size(f)
		if err != nil {
			t.Fatal(err)
		}
		f.Payload = nil
		got, err := l.SizeMetadata(f, uint64(n))
		if err != nil || got != want {
			t.Fatal(n, got, want, err)
		}
		if !bytes.Equal(payload, bytes.Repeat([]byte{0xa5}, n)) {
			t.Fatal("sizing mutated caller data")
		}
	}
	f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1}
	if _, err := l.SizeMetadata(f, wire.MaxData+1); err == nil {
		t.Fatal("oversize admitted")
	}
	if _, err := l.SizeMetadata(f, 0); err == nil {
		t.Fatal("empty DATA admitted")
	}
}
