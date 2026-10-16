package flow

import (
	"reflect"
	"testing"

	"itom.local/core/wire"
)

func checkStatusDiscoversFirstLastOnlyAndMultipleHoles(t *testing.T) {
	for _, c := range []struct {
		name      string
		received  []uint64
		attempted uint64
		want      []wire.ByteRange
	}{
		{"only", nil, 10, []wire.ByteRange{{Start: 0, End: 10}}},
		{"first", []uint64{10, 20}, 30, []wire.ByteRange{{Start: 0, End: 10}}},
		{"last", []uint64{0, 10}, 30, []wire.ByteRange{{Start: 20, End: 30}}},
		{"multiple", []uint64{10, 30}, 50, []wire.ByteRange{{Start: 0, End: 10}, {Start: 20, End: 30}, {Start: 40, End: 50}}},
		{"complete", []uint64{0, 10}, 20, nil},
	} {
		checkScenarioBoundary(t, c.name, func(t *testing.T) {
			r, err := NewRx(1024, 2, 16)
			if err != nil {
				t.Fatal(err)
			}
			if err = r.OpenStream(1, 100); err != nil {
				t.Fatal(err)
			}
			for _, offset := range c.received {
				if _, _, err = r.Receive(wire.Frame{Type: wire.Data, Stream: 1, Logical: offset + 1, Offset: offset, Payload: make([]byte, 10)}); err != nil {
					t.Fatal(err)
				}
			}
			before := r.Snapshot()
			ranges, err := r.Missing(1, c.attempted, 32)
			if err != nil || !reflect.DeepEqual(ranges, c.want) {
				t.Fatal(ranges, err)
			}
			if r.Snapshot() != before {
				t.Fatal("STATUS consumed credit")
			}
			if err = r.Check(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func checkMissingBoundsAndReorderedStatus(t *testing.T) {
	r, err := NewRx(1024, 2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.OpenStream(1, 100); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []uint64{10, 30} {
		if _, _, err = r.Receive(wire.Frame{Type: wire.Data, Stream: 1, Logical: offset + 1, Offset: offset, Payload: make([]byte, 10)}); err != nil {
			t.Fatal(err)
		}
	}
	ranges, err := r.Missing(1, 50, 1)
	if err != nil || !reflect.DeepEqual(ranges, []wire.ByteRange{{Start: 0, End: 10}}) {
		t.Fatal(ranges, err)
	}
	for _, args := range [][2]uint64{{101, 1}, {50, 0}, {50, 33}} {
		if _, err = r.Missing(1, args[0], int(args[1])); err == nil {
			t.Fatal(args)
		}
	}
	if _, _, err = r.Receive(wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: make([]byte, 10)}); err != nil {
		t.Fatal(err)
	}
	if ranges, err = r.Missing(1, 5, 32); err != nil || len(ranges) != 0 {
		t.Fatal("stale STATUS", ranges, err)
	}
}
func checkAttemptedEndExcludesQueuedAndSurvivesReceipt(t *testing.T) {
	tx, err := NewTx(DefaultLimits(), map[string]uint64{"a": 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	if err = tx.UpdateCredit(1, wire.Credit{MaxStream: 1024, MaxData: 1024}); err != nil {
		t.Fatal(err)
	}
	id, err := tx.Admit(1, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := tx.AttemptedEnd(1); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if err = tx.Reserve(id, "a"); err != nil {
		t.Fatal(err)
	}
	attempt, err := tx.Begin(id, 512)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Complete(attempt.ID, CommitUnknown); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Receipt(1, 5); err != nil {
		t.Fatal(err)
	}
	if n, err := tx.AttemptedEnd(1); err != nil || n != 5 {
		t.Fatal(n, err)
	}
	if tx.Snapshot().PhysicalExposure != 512 {
		t.Fatal("receipt cleared physical debt")
	}
}
func checkCreditSnapshotsAdvanceIndependentlyAndAtomically(t *testing.T) {
	tx, err := NewTx(DefaultLimits(), map[string]uint64{"a": 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.OpenStream(1); err != nil {
		t.Fatal(err)
	}
	for _, c := range []wire.Credit{{MaxStream: 100, MaxData: 200}, {MaxStream: 80, MaxData: 300}, {MaxStream: 150, MaxData: 250}, {MaxStream: 150, MaxData: 300}} {
		if err = tx.UpdateCredit(1, c); err != nil {
			t.Fatal(err)
		}
	}
	if tx.streams[1].peerMax != 150 || tx.peerMaxData != 300 {
		t.Fatal("window regressed")
	}
	if err = tx.UpdateCredit(1, wire.Credit{MaxStream: 200, MaxData: wire.MaxOffset + 1}); err == nil {
		t.Fatal("overflow accepted")
	}
	if tx.streams[1].peerMax != 150 || tx.peerMaxData != 300 {
		t.Fatal("partial mutation")
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioStatus(t *testing.T) {
	t.Log("check=checkStatusDiscoversFirstLastOnlyAndMultipleHoles")
	checkStatusDiscoversFirstLastOnlyAndMultipleHoles(t)
	t.Log("check=checkMissingBoundsAndReorderedStatus")
	checkMissingBoundsAndReorderedStatus(t)
	t.Log("check=checkAttemptedEndExcludesQueuedAndSurvivesReceipt")
	checkAttemptedEndExcludesQueuedAndSurvivesReceipt(t)
	t.Log("check=checkCreditSnapshotsAdvanceIndependentlyAndAtomically")
	checkCreditSnapshotsAdvanceIndependentlyAndAtomically(t)
}
