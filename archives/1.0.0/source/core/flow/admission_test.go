package flow

import (
	"itom.local/core/wire"
	"testing"
)

func TestAdmissionUsesExactOddCredit(t *testing.T) {
	tx, e := NewTx(DefaultLimits(), map[string]uint64{"a01": 1 << 20})
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.OpenStream(1); e != nil {
		t.Fatal(e)
	}
	if e = tx.ConnectionLimit(10000); e != nil {
		t.Fatal(e)
	}
	if e = tx.StreamLimit(1, 997); e != nil {
		t.Fatal(e)
	}
	n := tx.AdmissionCapacity(1)
	if n != 997 {
		t.Fatal(n)
	}
	if _, e = tx.Admit(1, make([]byte, n)); e != nil {
		t.Fatal(e)
	}
	if tx.AdmissionCapacity(1) != 0 || len(tx.items) != 1 {
		t.Fatal("odd credit fragmented")
	}
	if e = tx.StreamLimit(1, 20000); e != nil {
		t.Fatal(e)
	}
	if tx.AdmissionCapacity(1) != 9003 {
		t.Fatal("connection credit not bounded")
	}
	if tx.AdmissionCapacity(3) != 0 {
		t.Fatal("unknown stream admitted")
	}
	if e = tx.ConnectionLimit(wire.MaxOffset); e != nil {
		t.Fatal(e)
	}
	if e = tx.StreamLimit(1, wire.MaxOffset); e != nil {
		t.Fatal(e)
	}
	if tx.AdmissionCapacity(1) != wire.MaxData {
		t.Fatal("frame limit not bounded")
	}
}
