package wire

import "testing"

func TestProgressMergePreservesIndependentFrontiersAndFinal(t *testing.T) {
	a, _ := EncodeControl(7, Progress{Attempted: 100, Receipt: 20, MaxStream: 200, MaxData: 300, HasFinal: true, Final: 100})
	b, _ := EncodeControl(7, Progress{Receipt: 80, MaxStream: 260, MaxData: 900})
	for _, order := range [][2]Frame{{a, b}, {b, a}} {
		merged, ok := MergeProgressFrames(order[0], order[1])
		if !ok {
			t.Fatal("valid independent frontiers rejected")
		}
		got, err := DecodeControl(merged)
		want := Progress{Attempted: 100, Receipt: 80, MaxStream: 260, MaxData: 900, HasFinal: true, Final: 100}
		if err != nil || got != want {
			t.Fatal(got, err)
		}
	}
	b.Stream = 8
	if _, ok := MergeProgressFrames(a, b); ok {
		t.Fatal("mixed streams merged")
	}
	b, _ = EncodeControl(7, Progress{Attempted: 101, MaxStream: 200, MaxData: 300})
	if _, ok := MergeProgressFrames(a, b); ok {
		t.Fatal("frontier beyond immutable final merged")
	}
	b, _ = EncodeControl(7, Progress{Attempted: 99, MaxStream: 200, MaxData: 300, HasFinal: true, Final: 99})
	if _, ok := MergeProgressFrames(a, b); ok {
		t.Fatal("conflicting finals merged")
	}
	b = a
	b.Payload = nil
	if _, ok := MergeProgressFrames(a, b); ok {
		t.Fatal("malformed status merged")
	}
}
