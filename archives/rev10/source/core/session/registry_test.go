package session

import (
	"errors"
	"testing"
)

func checkReorderedOpenAndCompactedTombstones(t *testing.T) {
	r, err := NewRegistry(4, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{5, 1, 3} {
		if _, err = r.Open(id); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.Close(5); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(3); err != nil {
		t.Fatal(err)
	}
	for id := uint64(7); id < 20001; id += 2 {
		if _, err = r.Open(id); err != nil {
			t.Fatal(id, err)
		}
		if err = r.Close(id); err != nil {
			t.Fatal(err)
		}
	}
	a, c := r.Counts()
	if a != 1 || c != 1 {
		t.Fatal(a, c)
	}
	if _, err = r.Open(5); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}
func checkTombstoneSpaceReservedBeforeOpen(t *testing.T) {
	r, err := NewRegistry(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uint64{1, 5} {
		if _, err = r.Open(id); err != nil {
			t.Fatal(err)
		}
	}
	if err = r.Close(1); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(5); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Open(9); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
}
func checkStopRejectsLateWorkersAndNewGeneration(t *testing.T) {
	var l Lifecycle
	first, err := l.Start()
	if err != nil {
		t.Fatal(err)
	}
	l.Stop()
	if l.Accept(first) || l.Ready() {
		t.Fatal("accepted after stop")
	}
	second, err := l.Start()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("generation reused")
	}
	if err = l.Advance(first, Handshaking, Confirming); err == nil {
		t.Fatal("late completion")
	}
	if err = l.Advance(second, Handshaking, Confirming); err != nil {
		t.Fatal(err)
	}
	if l.Ready() {
		t.Fatal("connected before READY")
	}
	if err = l.Advance(second, Confirming, Active); err != nil {
		t.Fatal(err)
	}
	if !l.Ready() {
		t.Fatal("not active")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioRegistry(t *testing.T) {
	t.Log("check=checkReorderedOpenAndCompactedTombstones")
	checkReorderedOpenAndCompactedTombstones(t)
	t.Log("check=checkTombstoneSpaceReservedBeforeOpen")
	checkTombstoneSpaceReservedBeforeOpen(t)
	t.Log("check=checkStopRejectsLateWorkersAndNewGeneration")
	checkStopRejectsLateWorkersAndNewGeneration(t)
}
