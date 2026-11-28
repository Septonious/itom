package v160

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestV160ReliableSpanSurvivesFeedbackOvertaking(t *testing.T) {
	c, s := pair(t)
	a, b := NewReliableChannel(c), NewReliableChannel(s)
	old, id, e := a.Send([]Frame{{Kind: Data, Stream: 1, Payload: []byte("old")}})
	if e != nil {
		t.Fatal(e)
	}
	// A reverse record is received and acknowledged many times, while the
	// old forward DATA is still missing. This deliberately exceeds the span.
	reverse, rid, e := b.Send([]Frame{{Kind: Ping, Offset: 9}})
	if e != nil {
		t.Fatal(e)
	}
	if r, e := a.Receive(reverse, func([]Frame) error { return nil }); e != nil || !r.NeedsReceipt {
		t.Fatal(r, e)
	}
	var fb []byte
	for i := 0; i < ReplaySpan+100; i++ {
		fb, _, e = a.Feedback([]uint64{rid})
		if e != nil {
			t.Fatal(i, e)
		}
	}
	if r, e := b.Receive(fb, nil); e != nil || r.NeedsReceipt || !reflect.DeepEqual(r.Acknowledged, []uint64{rid}) {
		t.Fatal(r, e)
	}
	if _, _, e = a.Send([]Frame{{Kind: Ping, Offset: 1}}); !errors.Is(e, ErrCapacity) {
		t.Fatal("new reliable escaped span", e)
	}
	if w, ok := a.Repair(id); !ok || !bytes.Equal(w, old) {
		t.Fatal("old record unavailable")
	}
	applied := 0
	if r, e := b.Receive(old, func(fs []Frame) error { applied++; return nil }); e != nil || !r.NeedsReceipt {
		t.Fatal(r, e)
	}
	ack, _, e := b.Feedback([]uint64{id})
	if e != nil {
		t.Fatal(e)
	}
	if r, e := a.Receive(ack, nil); e != nil || !reflect.DeepEqual(r.Acknowledged, []uint64{id}) {
		t.Fatal(r, e)
	}
	next, nid, e := a.Send([]Frame{{Kind: Data, Stream: 1, Offset: 3, Payload: []byte("new")}})
	if e != nil || nid < ReplaySpan {
		t.Fatal(nid, e)
	}
	if r, e := b.Receive(next, func(fs []Frame) error { applied++; return nil }); e != nil || r.Duplicate {
		t.Fatal(r, e)
	}
	if r, e := b.Receive(old, func(fs []Frame) error { t.Fatal("old duplicate applied"); return nil }); e != nil || !r.Duplicate || !r.NeedsReceipt {
		t.Fatal(r, e)
	}
	if applied != 2 {
		t.Fatal(applied)
	}
	if r, e := a.Receive(ack, nil); e != nil || len(r.Acknowledged) != 0 {
		t.Fatal("duplicate ack advanced credit", r, e)
	}
}
func TestV160ReliableAtomicAcceptanceAndImmutableRepair(t *testing.T) {
	c, s := pair(t)
	a, b := NewReliableChannel(c), NewReliableChannel(s)
	fs := []Frame{{Kind: Data, Stream: 1, Payload: []byte("a")}, {Kind: Data, Stream: 3, Payload: []byte("b")}}
	w, id, e := a.Send(fs)
	if e != nil {
		t.Fatal(e)
	}
	w[8] ^= 1
	if _, e = b.Receive(w, nil); e == nil {
		t.Fatal("tampering accepted")
	}
	w, ok := a.Repair(id)
	if !ok {
		t.Fatal("immutable original gone")
	}
	if r, e := b.Receive(w, func([]Frame) error { return ErrCapacity }); !errors.Is(e, ErrCapacity) || r.NeedsReceipt {
		t.Fatal(r, e)
	}
	if r, e := b.Receive(w, func(g []Frame) error {
		if !reflect.DeepEqual(g, fs) {
			t.Fatal(g)
		}
		return nil
	}); e != nil || r.Duplicate || !r.NeedsReceipt {
		t.Fatal(r, e)
	}
	if n, _ := a.Pending(); n != 1 {
		t.Fatal("APPEND not a receipt")
	}
	ack, _, e := b.Feedback([]uint64{id})
	if e != nil {
		t.Fatal(e)
	}
	if r, e := a.Receive(ack, nil); e != nil || len(r.Acknowledged) != 1 {
		t.Fatal(r, e)
	}
	if n, nbytes := a.Pending(); n != 0 || nbytes != 0 {
		t.Fatal(n, nbytes)
	}
}
func TestV160ReceiptCanonicalRanges(t *testing.T) {
	p, e := EncodeReceipts([]uint64{7, 2, 3, 4, 7, 9})
	if e != nil {
		t.Fatal(e)
	}
	ids, e := DecodeReceipts(p)
	if e != nil || !reflect.DeepEqual(ids, []uint64{2, 3, 4, 7, 9}) {
		t.Fatal(ids, e)
	}
	for _, p := range [][]byte{{0, 0}, {1, 1, 2, 1}, {1, 3, 2, 1}, {0x80, 0, 1}, {0, 0x80, 0x40}} {
		if _, e := DecodeReceipts(p); e == nil {
			t.Fatalf("accepted %x", p)
		}
	}
}

func TestV160InvalidReceiptIsAtomic(t *testing.T) {
	c, s := pair(t)
	a := NewReliableChannel(c)
	_, id, e := a.Send([]Frame{{Kind: Ping, Offset: 9}})
	if e != nil {
		t.Fatal(e)
	}
	payload, e := EncodeReceipts([]uint64{id, id + 1})
	if e != nil {
		t.Fatal(e)
	}
	wire, _, e := s.Seal([]Frame{{Kind: SequenceFloor, Offset: 0}, {Kind: Receipt, Payload: payload}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Receive(wire, nil); !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
	if n, _ := a.Pending(); n != 1 {
		t.Fatal("invalid receipt released pending record")
	}
}

func FuzzV160ReceiptRanges(f *testing.F) {
	f.Add([]byte{0, 1, 2, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		ids, e := DecodeReceipts(b)
		if e != nil {
			return
		}
		enc, e := EncodeReceipts(ids)
		if e != nil || !bytes.Equal(b, enc) {
			t.Fatal("noncanonical receipt")
		}
	})
}
