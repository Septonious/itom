package mailstream

import "testing"

func TestV160ReceiptQueueReserveAndCoalescing(t *testing.T) {
	q := newReceiptQueue(4, 1)
	for i := uint64(0); i < 3; i++ {
		if !q.canAccept(i, true) || !q.offer(receiptRequest{i, true}) {
			t.Fatal("bulk reserve")
		}
	}
	if q.canAccept(3, true) || q.offer(receiptRequest{3, true}) {
		t.Fatal("bulk consumed urgent reserve")
	}
	if !q.offer(receiptRequest{3, false}) {
		t.Fatal("urgent refused")
	}
	for range 10 {
		if !q.offer(receiptRequest{3, false}) {
			t.Fatal("duplicate refused")
		}
	}
	if q.size() != 4 {
		t.Fatal(q.size())
	}
	b := q.take(nil, 2)
	if len(b) != 2 || b[0].sequence != 3 || b[0].bulk {
		t.Fatal(b)
	}
	if q.size() != 4 || q.canAccept(4, false) {
		t.Fatal("inflight receipt capacity prematurely released")
	}
	q.complete(b)
	if q.size() != 2 || !q.offer(receiptRequest{4, true}) {
		t.Fatal("completion did not return capacity")
	}
}
