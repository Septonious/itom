package datapath

import (
	"itom.local/core/flow"
	"itom.local/core/wire"
	"testing"
	"time"
)

// A chat can exceed 64 KiB across many serial request/reply turns without
// becoming a sustained fragmented bulk stream. Traffic volume must have a
// bounded observation window, rather than accumulating until a two-second gap.
func TestRev5LongLivedChatDoesNotAccumulatePermanentBulkClassification(t *testing.T) {
	a, _ := pair(t)
	for turn := 0; turn < 100; turn++ {
		if err := a.clock(time.Duration(turn) * 400 * time.Millisecond); err != nil {
			t.Fatal(err)
		}
		max := uint64((turn+1)*(4<<10) + (64 << 10))
		if _, err := a.Receive(1, control(t, 1, wire.Credit{MaxStream: max, MaxData: max})); err != nil {
			t.Fatal(err)
		}
		id, err := a.Queue(1, 1, make([]byte, 4<<10))
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.Begin(1, id, "a01", 6000, a.now)
		if err != nil {
			t.Fatal(err)
		}
		if !job.Interactive() {
			t.Fatal("serial chat became bulk despite only 10 KiB/s", turn)
		}
		if err := a.Complete(1, job.Attempt().ID, flow.Committed); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Receive(1, control(t, 1, wire.Received{Frontier: uint64(turn+1) * (4 << 10)})); err != nil {
			t.Fatal(err)
		}
	}
}
