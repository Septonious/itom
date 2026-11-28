package mailstream

import (
	"reflect"
	"testing"
)

func TestV160FairBulkDispatch(t *testing.T) {
	packet := func(seq uint64, id uint32, bytes int, urgent bool) dispatchCandidate {
		return dispatchCandidate{seq, &pendingPacket{stream: id, kind: frameData, priority: urgent, wire: make([]byte, bytes)}}
	}
	original := []dispatchCandidate{packet(9, 9, 100, true), packet(10, 1, 32768, false), packet(11, 1, 32768, false), packet(12, 1, 32768, false), packet(13, 1, 32768, false), packet(14, 3, 32768, false), packet(15, 3, 32768, false), packet(16, 3, 32768, false)}
	for _, tc := range []struct {
		after uint32
		want  []uint64
	}{
		{0, []uint64{9, 10, 11, 14, 15, 12, 13, 16}},
		{1, []uint64{9, 14, 15, 10, 11, 16, 12, 13}},
		{3, []uint64{9, 10, 11, 14, 15, 12, 13, 16}},
	} {
		b := append([]dispatchCandidate(nil), original...)
		fairBulkOrder(b, tc.after, map[uint64]dispatchRank{}, map[uint32]int{})
		var got []uint64
		for _, c := range b {
			got = append(got, c.sequence)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("after %d got %v want %v", tc.after, got, tc.want)
		}
	}
	b := append([]dispatchCandidate(nil), original[:5]...)
	fairBulkOrder(b, 1, map[uint64]dispatchRank{}, map[uint32]int{})
	if !reflect.DeepEqual(b, original[:5]) {
		t.Fatal("single stream changed")
	}
}
