package mailstream

import (
	"reflect"
	"testing"
	"time"
)

func TestV160BulkPublicationOrder(t *testing.T) {
	now := time.Unix(1234, 0)
	l := &IMAPLink{}
	var allowed [20]bool
	for i, delay := range []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 300 * time.Millisecond} {
		a := &mailAccountState{ready: true, writer: &mailPipe{}}
		a.bulkService.observe(100000, delay, now)
		l.accounts = append(l.accounts, a)
		allowed[i] = true
	}
	l.accounts[0].bulkInFlight.Store(400000)
	if got := l.bulkPublicationOrder(0, allowed, nil, 100000, now); !reflect.DeepEqual(got, []int{1, 0, 2}) {
		t.Fatalf("queue and in-flight debt: %v", got)
	}
	l.accounts[0].bulkInFlight.Store(0)
	if got := l.bulkPublicationOrder(0, allowed, nil, 100000, now); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("fast path recovered: %v", got)
	}
	if got := l.bulkPublicationOrder(0, allowed, l.accounts[0], 100000, now); got[0] != 1 || got[2] != 0 {
		t.Fatalf("exclusion: %v", got)
	}
	if got := l.bulkPublicationOrder(0, allowed, nil, 100000, now.Add(16*time.Second)); got != nil {
		t.Fatalf("stale measurements rank: %v", got)
	}
}

func TestV160BulkReceiptRankingAndEvidence(t *testing.T) {
	now := time.Now()
	id := SessionID{1}
	l := &IMAPLink{}
	var allowed [20]bool
	for i, delay := range []time.Duration{20 * time.Millisecond, 25 * time.Millisecond, 30 * time.Millisecond} {
		a := &mailAccountState{ready: true, writer: &mailPipe{}, id: id}
		a.bulkService.observe(100000, delay, now)
		l.accounts = append(l.accounts, a)
		allowed[i] = true
		for j := 0; j < 4; j++ {
			a.bulkReceipt.observe([]time.Duration{300 * time.Millisecond, 75 * time.Millisecond, 80 * time.Millisecond}[i], now)
		}
	}
	if got := l.bulkPublicationOrder(0, allowed, nil, 100000, now); got[0] != 1 {
		t.Fatalf("receipt tails did not influence rank: %v", got)
	}
	a := l.accounts[0]
	before := a.bulkReceipt.samples
	a.delivery[1] = deliverySample{id: id, sequence: 1, started: now.Add(-time.Second), bulk: true}
	a.delivery[2] = deliverySample{id: id, sequence: 2, started: now.Add(-time.Second), bulk: true, retried: true}
	a.delivery[3] = deliverySample{id: id, sequence: 3, started: now.Add(-time.Second), bulk: true}
	if err := l.AcknowledgeBatchEvidence(id, []uint64{1, 2, 3}, []uint64{2, 3}); err != nil {
		t.Fatal(err)
	}
	if a.bulkReceipt.samples != before+1 {
		t.Fatalf("ambiguous attempt trained estimator: %d -> %d", before, a.bulkReceipt.samples)
	}
	if a.delivery[1] != (deliverySample{}) || a.delivery[2] != (deliverySample{}) || a.delivery[3] != (deliverySample{}) {
		t.Fatal("excluded timing evidence did not retire delivery")
	}
}
