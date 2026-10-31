package transport_test

import (
	"bytes"
	"context"
	"errors"
	"itom.local/core/transport"
	"testing"
	"time"
)

func TestBulkPublicationCombinesReadyRecordsUnderOnePhysicalOrdinal(t *testing.T) {
	f := publicationsOn(t, 2<<20, 16, transport.Bulk)
	payload := bytes.Repeat([]byte("abcdefgh"), 64<<10)
	if err := f.sender.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	if p.Usage().Total.Messages != 1 || f.sender.Snapshot().RetainedRecords < 2 {
		t.Fatal("bulk still spends one physical ordinal per record")
	}
	delivery, err := f.incoming.Decode(9, 1, p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	received := make([]byte, len(payload))
	n, err := f.receiver.Read(1, received)
	if err != nil || n != len(payload) || !bytes.Equal(received, payload) {
		t.Fatal("batch lost bytes", n, err)
	}
	if f.sender.Snapshot().SendBuffered != uint64(len(payload)) {
		t.Fatal("APPEND/body decode fabricated peer delivery")
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Used.Messages != 1 || len(entries[0].Pending) != 1 {
		t.Fatal("physical accounting multiplied by record count", err)
	}
}
func TestBulkBatchNeverWaitsToFillOrConsumesShortFastWork(t *testing.T) {
	f := publicationsOn(t, 2<<20, 16, transport.Bulk)
	// Large enough to be bulk, but far smaller than the maximum physical body.
	payload := bytes.Repeat([]byte{3}, 40<<10)
	if err := f.sender.Queue(1, payload, time.Now()); err != nil {
		t.Fatal(err)
	}
	add(t, f.sender, 3, 1<<20, 1<<20)
	if err := f.sender.Queue(3, []byte("short request"), time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
	if err != nil || p == nil {
		t.Fatal("partial batch waited for a collector", err)
	}
	delivery, err := f.incoming.Decode(9, 1, p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	d, ok := f.sender.TakeFor(transport.Fast, transport.MaxPayload, transport.DataLedger)
	if !ok || d.Record.Stream != 3 {
		t.Fatal("bulk borrowed fast work")
	}
	complete(t, d, transport.Accepted)
}
func TestBatchUsesExactPartiallyAvailableGrantWithoutFencingIt(t *testing.T) {
	f := publicationsOn(t, 10000, 16, transport.Bulk)
	if err := f.sender.Queue(1, bytes.Repeat([]byte{4}, 128<<10), time.Now()); err != nil {
		t.Fatal(err)
	}
	p, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	if uint64(len(p.MIME())) != p.Usage().Attempt.EncodedBytes || p.Usage().Total.Bytes > 10000 {
		t.Fatal("batch exceeded concrete remaining permission")
	}
	delivery, err := f.incoming.Decode(9, 1, p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	entries, err := f.wallet.Entries()
	if err != nil || entries[0].Fenced {
		t.Fatal("ordinary batching fenced a valid grant", err)
	}
}
func TestClosingOneFlowInsideBatchCannotResetOrDropAnotherFlow(t *testing.T) {
	f := publicationsOn(t, 2<<20, 16, transport.Bulk)
	add(t, f.sender, 3, 1<<20, 1<<20)
	add(t, f.receiver, 3, 1<<20, 1<<20)
	first := bytes.Repeat([]byte{1}, 128<<10)
	second := bytes.Repeat([]byte{2}, 128<<10)
	f.sender.Queue(1, first, time.Now())
	f.sender.Queue(3, second, time.Now())
	p, err := f.publisher.PrepareBulk(context.Background(), transport.MaxBulkBytes)
	if err != nil || p == nil {
		t.Fatal(err)
	}
	closed, err := f.receiver.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	delivery, err := f.incoming.Decode(9, 1, p.MIME())
	if err != nil {
		t.Fatal(err)
	}
	if err = delivery.Apply(); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(second))
	n, err := f.receiver.Read(3, got)
	if err != nil || n != len(second) || !bytes.Equal(got, second) {
		t.Fatal("canceled batch member broke another stream", n, err)
	}
	if !delivery.Discarded() {
		t.Fatal("closed member was not reported")
	}
}
func TestBulkDispatchRetryIsBoundedAndUnknownCannotBeRequeued(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	payload := bytes.Repeat([]byte{8}, 512<<10)
	s.Queue(1, payload, time.Now())
	batch, ok := s.TakeBulk(transport.MaxBulkBytes)
	if !ok {
		t.Fatal("batch missing")
	}
	before := batch.Records()
	if err := batch.Complete(transport.NotStarted); err != nil {
		t.Fatal(err)
	}
	smaller, ok := s.TakeBulk(15000)
	if ok {
		for _, r := range smaller.Records() {
			if len(r.Payload) > 15000 {
				t.Fatal("retry exceeded the new ticket")
			}
		}
		smaller.Complete(transport.NotStarted)
	}
	retry, ok := s.TakeBulk(transport.MaxBulkBytes)
	if !ok {
		t.Fatal("unsent batch was lost")
	}
	after := retry.Records()
	if len(after) != len(before) {
		t.Fatal("unsent record count changed", len(before), len(after))
	}
	for i := range before {
		if before[i].Offset != after[i].Offset || !bytes.Equal(before[i].Payload, after[i].Payload) {
			t.Fatal("unsent retry changed logical bytes")
		}
	}
	if err := retry.Complete(transport.Uncertain); err != nil {
		t.Fatal(err)
	}
	if err := retry.Complete(transport.NotStarted); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("unknown body became retryable", err)
	}
	if s.PendingPublication(1) != len(after) {
		t.Fatal("uncertain records lost recovery ownership")
	}
}
