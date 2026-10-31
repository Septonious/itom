package transport_test

import (
	"bytes"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestReadingTLSFragmentsDoesNotSpendAnotherControlMailWhileCreditRemains(t *testing.T) {
	for _, size := range []int{1, 29, 1500, 16 << 10} {
		a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
		add(t, a, 1, 256<<10, 256<<10)
		add(t, b, 1, 256<<10, 256<<10)
		payload := bytes.Repeat([]byte{17}, size)
		if err := a.Queue(1, payload, time.Now()); err != nil {
			t.Fatal(err)
		}
		data, ok := next(t, a, transport.Fast, transport.MaxPayload)
		if !ok || b.Receive(data) != nil {
			t.Fatal("initial fragment was not immediately eligible", size)
		}
		// Deliberately let the writer send receipt before the application reads,
		// the schedule that previously manufactured an extra credit publication.
		ack, ok := next(t, b, transport.Fast, transport.MaxPayload)
		if !ok || ack.Kind != transport.Receipt || a.Receive(ack) != nil {
			t.Fatal("prompt cumulative receipt lost", size, ack)
		}
		got := make([]byte, size)
		if n, err := b.Read(1, got); n != size || err != nil || !bytes.Equal(got, payload) {
			t.Fatal("fragment delivery changed", size, n, err)
		}
		if b.ReadyFor(transport.Fast, transport.ControlLedger) {
			t.Fatal("reading a fragment spent redundant control permission", size)
		}
		if err := b.Queue(1, []byte("reply"), time.Now()); err != nil {
			t.Fatal(err)
		}
		reply, ok := next(t, b, transport.Fast, transport.MaxPayload)
		if !ok || reply.Kind != transport.Data || reply.Acknowledged != uint64(size) || reply.ReceiveLimit != 256<<10+uint64(size) || a.Receive(reply) != nil {
			t.Fatal("reply did not carry latest backed credit immediately", size, reply)
		}
	}
}

func TestOneWayFragmentedTransferRenewsCreditWithoutWaitingForAReplyOrTimer(t *testing.T) {
	a, b := session(t, transport.DefaultLimits()), session(t, transport.DefaultLimits())
	const window = 32 << 10
	add(t, a, 1, window, window)
	add(t, b, 1, window, window)
	payload := bytes.Repeat([]byte{23}, 1024)
	var delivered uint64
	credits := 0
	for range 4 * window / len(payload) {
		if err := a.Queue(1, payload, time.Now()); err != nil {
			t.Fatal(err)
		}
		data, ok := next(t, a, transport.Fast, transport.MaxPayload)
		if !ok {
			data, ok = next(t, a, transport.Bulk, transport.MaxPayload)
		}
		if !ok || data.Kind != transport.Data || b.Receive(data) != nil {
			t.Fatal("one-way sender stalled with a consuming receiver", delivered)
		}
		got := make([]byte, len(payload))
		if n, err := b.Read(1, got); err != nil || n != len(payload) || !bytes.Equal(got, payload) {
			t.Fatal("one-way payload changed", delivered, n, err)
		}
		delivered += uint64(len(payload))
		ack, ok := next(t, b, transport.Fast, transport.MaxPayload)
		if !ok || a.Receive(ack) != nil {
			t.Fatal("one-way progress feedback missing", delivered)
		}
		if ack.Kind == transport.Credit {
			credits++
		}
		if b.ReadyFor(transport.Fast, transport.ControlLedger) {
			t.Fatal("one receive/read required multiple progress publications", delivered)
		}
	}
	if credits == 0 || credits >= 4*window/len(payload) || a.Snapshot().SendBuffered != 0 {
		t.Fatal("credit never renewed, renewed per fragment, or retained acknowledged flight", credits, a.Snapshot())
	}
}
