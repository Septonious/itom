package mailstream

import (
	"cmp"
	"slices"
)

// Bulk dispatch is byte-fair across the streams that have eligible DATA.
// Keep each stream's record order and preserve the control prefix. A rotating
// cursor makes a bounded dispatch wake fair even if the carrier fills early.
const dispatchQuantum = 64 << 10

type dispatchCandidate struct {
	sequence uint64
	packet   *pendingPacket
}

type dispatchRank struct {
	stream uint32
	round  int
}

func fairBulkOrder(batch []dispatchCandidate, after uint32, ranks map[uint64]dispatchRank, used map[uint32]int) {
	first := 0
	for first < len(batch) && batch[first].packet.priority {
		first++
	}
	// A single stream is common during a large download. Do not allocate or
	// reorder it. Likewise preserve a non-DATA publication's established order.
	if len(batch)-first < 2 {
		return
	}
	stream := batch[first].packet.stream
	multiple := false
	for _, c := range batch[first:] {
		if c.packet.kind != frameData {
			return
		}
		multiple = multiple || c.packet.stream != stream
	}
	if !multiple {
		return
	}
	clear(ranks)
	clear(used)
	for _, c := range batch[first:] {
		id := c.packet.stream
		ranks[c.sequence] = dispatchRank{id, used[id] / dispatchQuantum}
		used[id] += len(c.packet.wire)
	}
	slices.SortFunc(batch[first:], func(a, b dispatchCandidate) int {
		x, y := ranks[a.sequence], ranks[b.sequence]
		if x.round != y.round {
			return cmp.Compare(x.round, y.round)
		}
		// unsigned wrap is the rotating stream-id order.
		if x.stream != y.stream {
			return cmp.Compare(x.stream-after-1, y.stream-after-1)
		}
		return cmp.Compare(a.sequence, b.sequence)
	})
}
