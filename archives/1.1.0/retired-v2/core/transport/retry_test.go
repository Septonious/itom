package transport_test

import (
	"bytes"
	"itom.local/core/transport"
	"testing"
	"time"
)

func TestUnstartedBurstsRemainReachableAfterMoreThanOneDispatchWindow(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	if err := s.Queue(1, bytes.Repeat([]byte{7}, 1<<20), time.Now()); err != nil {
		t.Fatal(err)
	}
	expected := make(map[uint64]int)
	for _, size := range []int{512, 256} {
		var wave []*transport.Dispatch
		for {
			d, ok := s.TakeFor(transport.Bulk, size, transport.DataLedger)
			if !ok {
				break
			}
			wave = append(wave, d)
			expected[d.Record.Offset] = len(d.Record.Payload)
		}
		if len(wave) == 0 {
			t.Fatal("test failed to fill a dispatch window")
		}
		for _, d := range wave {
			complete(t, d, transport.NotStarted)
		}
	}
	if len(expected) <= 256 {
		t.Fatal("did not exercise the former retry limit", len(expected))
	}
	for range len(expected) {
		d := take(t, s, transport.Bulk, 512)
		n, ok := expected[d.Record.Offset]
		if !ok || n != len(d.Record.Payload) {
			t.Fatal("unstarted record was lost or duplicated", d.Record.Offset)
		}
		delete(expected, d.Record.Offset)
		complete(t, d, transport.Accepted)
	}
	if len(expected) != 0 {
		t.Fatal("unreachable unpublished records", len(expected))
	}
}
func TestUnstartedCumulativeControlsCoalesceWithoutLosingLatestProgress(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	if err := s.Receive(incoming(transport.Data, 1, 0, []byte("a"))); err != nil {
		t.Fatal(err)
	}
	one := take(t, s, transport.Fast, 0)
	if err := s.Receive(incoming(transport.Data, 1, 1, []byte("b"))); err != nil {
		t.Fatal(err)
	}
	two := take(t, s, transport.Fast, 0)
	complete(t, one, transport.NotStarted)
	complete(t, two, transport.NotStarted)
	latest := take(t, s, transport.Fast, 0)
	if latest.Record.Kind != transport.Receipt || latest.Record.Offset != 2 {
		t.Fatal("coalescing lost cumulative progress", latest.Record)
	}
	complete(t, latest, transport.Accepted)
	if s.ReadyFor(transport.Fast, transport.ControlLedger) {
		t.Fatal("obsolete receipt remained queued")
	}
}
