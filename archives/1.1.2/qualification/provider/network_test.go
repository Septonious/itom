package main

import (
	"testing"
	"time"
)

func TestSharedRateDipChargesQueuedBytesAcrossBothBoundaries(t *testing.T) {
	start := time.Unix(1, 0)
	n := network{start: start, dipAfter: 10 * time.Second, dipDuration: 15 * time.Second, dipBitsPerSecond: 1_000_000}
	// 10 Mbit before the dip, 15 Mbit during it, 25 Mbit after it.
	begin := start.Add(9900 * time.Millisecond)
	if got := n.serializationEnd(begin, 50_000_000, 100_000_000); !got.Equal(start.Add(25250 * time.Millisecond)) {
		t.Fatal("queued traffic bypassed rate collapse or recovery", got.Sub(start))
	}
	// A packet wholly in the dip takes 128 ms, rather than 1.28 ms.
	if got := n.serializationEnd(start.Add(12*time.Second), 128000, 100_000_000); !got.Equal(start.Add(12128 * time.Millisecond)) {
		t.Fatal("low-rate serialization", got.Sub(start))
	}
}
