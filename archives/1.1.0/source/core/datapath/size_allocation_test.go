package datapath

import (
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"testing"
)

func previousSize(f wire.Frame, key *wire.QuotaKey) (uint64, error) {
	raw, err := f.MarshalBinary()
	if err != nil {
		return 0, err
	}
	body := make([]byte, 2*wire.HeaderSize+len(raw)+wire.TagSize)
	if key != nil {
		body, err = wire.BindPhysical(*key, 1, wire.ID{1}, body)
		if err != nil {
			return 0, err
		}
	}
	encoded, err := mailbox.Encode(body)
	return uint64(len(encoded)), err
}
func TestSizeMatchesPhysicalEncoding(t *testing.T) {
	key := wire.QuotaKey{Store: wire.ID{1}, Epoch: 1, Grant: 1, Session: wire.ID{2}, User: "12345678", Account: "a01"}
	for _, n := range []int{1, 57, 32768, wire.MaxData} {
		f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: make([]byte, n)}
		for _, k := range []*wire.QuotaKey{nil, &key, &wire.QuotaKey{}} {
			got, e := Size(f, k)
			want, w := previousSize(f, k)
			if got != want || (e == nil) != (w == nil) {
				t.Fatal(n, got, want, e, w)
			}
		}
	}
	if _, err := Size(wire.Frame{}, nil); err == nil {
		t.Fatal("invalid frame accepted")
	}
}
func BenchmarkBulkDataSize(b *testing.B) {
	f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: make([]byte, wire.MaxData)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Size(f, nil); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkBulkDataSizePrevious(b *testing.B) {
	f := wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: make([]byte, wire.MaxData)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := previousSize(f, nil); err != nil {
			b.Fatal(err)
		}
	}
}
