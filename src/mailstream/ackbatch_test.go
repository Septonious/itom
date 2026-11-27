package mailstream

import (
	"testing"
	"time"
)

func TestBatchedReceiptRetirementKeepsOtherSessions(t *testing.T) {
	id, other := SessionID{1}, SessionID{2}
	makeAccount := func() *mailAccountState {
		a := &mailAccountState{segments: map[segmentKey]*segmentRecord{
			{session: id, generation: 1}:    {awaiting: map[uint64]bool{1: true, 2: true, 3: true}},
			{session: other, generation: 1}: {awaiting: map[uint64]bool{1: true, 2: true}},
		}}
		a.delivery[1] = deliverySample{id: id, sequence: 1, started: time.Now().Add(-time.Second)}
		a.delivery[2] = deliverySample{id: id, sequence: 2, started: time.Now().Add(-time.Second), retried: true}
		a.delivery[3] = deliverySample{id: other, sequence: 3, started: time.Now()}
		return a
	}
	a, b := makeAccount(), makeAccount()
	l := &IMAPLink{accounts: []*mailAccountState{a, b}}
	if err := l.AcknowledgeBatch(id, []uint64{1, 2, 1}); err != nil {
		t.Fatal(err)
	}
	for _, x := range l.accounts {
		if x.delivery[1] != (deliverySample{}) || x.delivery[2] != (deliverySample{}) || x.delivery[3].id != other {
			t.Fatal("wrong delivery retired")
		}
		if len(x.segments[segmentKey{session: id, generation: 1}].awaiting) != 1 || len(x.segments[segmentKey{session: other, generation: 1}].awaiting) != 2 {
			t.Fatal("wrong segment retired")
		}
		if x.rtt <= 0 {
			t.Fatal("no measured receipt")
		}
		prev := x.rtt
		if err := l.AcknowledgeBatch(id, []uint64{1, 2}); err != nil {
			t.Fatal(err)
		}
		if x.rtt != prev {
			t.Fatal("duplicate changed RTT")
		}
	}
}
