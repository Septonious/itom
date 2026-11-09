package main

import (
	"testing"
	"time"
)

func TestSharedRateDipChargesQueuedBytesAcrossBothBoundaries(t *testing.T) {
	start := time.Unix(1, 0)
	n := network{start: start, dipStart: start.Add(10 * time.Second), dipDuration: 15 * time.Second, dipBitsPerSecond: 1_000_000}
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

func TestPhaseRTTResetDoesNotRestartBandwidthDip(t *testing.T) {
	start := time.Unix(1, 0)
	n := network{start: start, dipStart: start.Add(10 * time.Second), dipDuration: 15 * time.Second, dipBitsPerSecond: 1_000_000}
	n.start = start.Add(30 * time.Second)
	begin := start.Add(40 * time.Second)
	if got := n.serializationEnd(begin, 100_000_000, 100_000_000); !got.Equal(begin.Add(time.Second)) {
		t.Fatal("an RTT phase reset restarted the bandwidth collapse", got.Sub(begin))
	}
}

func TestRC117ExactSubOneMbpsSerializationBudget(t *testing.T) {
	start := time.Unix(1, 0)
	net := network{start: start}
	// The poor-mobile uplink must be shaped to 500,000 bit/s exactly,
	// not rounded to the old integer-only 1 Mbps fixture minimum.
	for _, tc := range []struct {
		rate int64
		want time.Duration
	}{
		{rate: 500_000, want: 256 * time.Millisecond},
		{rate: 2_000_000, want: 64 * time.Millisecond},
	} {
		got := net.serializationEnd(start, 128_000, tc.rate)
		if elapsed := got.Sub(start); elapsed != tc.want {
			t.Fatalf("rate=%d bps: got %s, want %s", tc.rate, elapsed, tc.want)
		}
	}
}
