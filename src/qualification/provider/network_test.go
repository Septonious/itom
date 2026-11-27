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
	// The new scenario charges upload and download independently and pauses
	// serialization across the explicit outage without resetting on /phase.
	n.scenarioStart = start
	n.scenario = []rateStage{{time.Second, 1_000_000, 10_000_000}, {time.Second, 0, 0}}
	if got := n.serializationEndDirection(start, 5_000_000, 10_000_000, true); !got.Equal(start.Add(500 * time.Millisecond)) {
		t.Fatal("downlink", got)
	}
	if got := n.serializationEndDirection(start, 2_000_000, 2_000_000, false); !got.Equal(start.Add(2500 * time.Millisecond)) {
		t.Fatal("uplink outage", got)
	}

}
