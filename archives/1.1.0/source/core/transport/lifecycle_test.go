package transport_test

import (
	"bytes"
	"errors"
	"io"
	"itom.local/core/transport"
	"os"
	"testing"
	"time"
)

func take(t *testing.T, s *transport.Session, plane transport.Plane, max int) *transport.Dispatch {
	t.Helper()
	d, ok := s.Take(plane, max)
	if !ok {
		t.Fatal("expected pending work")
	}
	return d
}
func complete(t *testing.T, d *transport.Dispatch, outcome transport.PublicationOutcome) {
	t.Helper()
	if err := d.Complete(outcome); err != nil {
		t.Fatal(err)
	}
}
func incoming(kind transport.Kind, id, offset uint64, b []byte) transport.Record {
	return transport.Record{Kind: kind, Generation: 7, Stream: id, Offset: offset, Payload: b}
}
func TestProviderAcceptanceDoesNotReleaseUnreceivedSendMemory(t *testing.T) {
	l := transport.DefaultLimits()
	l.SessionBytes = 1 << 20
	l.StreamBytes = 1 << 20
	s := session(t, l)
	add(t, s, 1, 1<<20, 1<<20)
	if err := s.Queue(1, make([]byte, 1<<20), time.Now()); err != nil {
		t.Fatal(err)
	}
	d := take(t, s, transport.Bulk, transport.MaxPayload)
	complete(t, d, transport.Accepted)
	if err := s.Queue(1, []byte{1}, time.Now()); !errors.Is(err, transport.ErrCapacity) {
		t.Fatal("APPEND acceptance prematurely freed replay data", err)
	}
	if err := s.Receive(incoming(transport.Receipt, 1, transport.MaxPayload-1, nil)); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("unbacked partial receipt accepted", err)
	}
	if err := s.Receive(incoming(transport.Receipt, 1, transport.MaxPayload, nil)); err != nil {
		t.Fatal(err)
	}
	if err := s.Queue(1, make([]byte, transport.MaxPayload), time.Now()); err != nil {
		t.Fatal("verified receiver progress did not release sender capacity", err)
	}
}
func TestUnstartedPublicationRequeuesImmutableBytesWithinNewAdmissionLimit(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	original := []byte("exact unsent record")
	if err := s.Queue(1, original, time.Now()); err != nil {
		t.Fatal(err)
	}
	d := take(t, s, transport.Fast, transport.MaxPayload)
	d.Record.Payload[0] ^= 0xff
	complete(t, d, transport.NotStarted)
	if _, ok := s.Take(transport.Fast, 2); ok {
		t.Fatal("retry exceeded concrete admission size")
	}
	d = take(t, s, transport.Fast, transport.MaxPayload)
	if !bytes.Equal(d.Record.Payload, original) || d.Record.Offset != 0 {
		t.Fatal("unsent bytes or offset changed", d.Record)
	}
	complete(t, d, transport.Accepted)
}
func TestUncertainPublicationCannotBecomeAnAutomaticPhysicalRetry(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	if err := s.Queue(1, []byte("uncertain"), time.Now()); err != nil {
		t.Fatal(err)
	}
	d := take(t, s, transport.Fast, transport.MaxPayload)
	complete(t, d, transport.Uncertain)
	if s.PendingPublication(1) != 1 {
		t.Fatal("unknown publication was forgotten")
	}
	for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
		if _, ok := s.Take(plane, transport.MaxPayload); ok {
			t.Fatal("unknown publication was automatically retried")
		}
	}
	if err := s.Receive(incoming(transport.Receipt, 1, 9, nil)); err != nil {
		t.Fatal(err)
	}
	if s.PendingPublication(1) != 0 {
		t.Fatal("authenticated delivery did not resolve logical replay memory")
	}
	if s.Snapshot().ActiveDispatches != 0 {
		t.Fatal("peer receipt leaked uncertain dispatch slots")
	}
}
func TestStreamCloseUnblocksBothDirectionsWithoutAffectingAnotherStream(t *testing.T) {
	l := transport.DefaultLimits()
	l.StreamBytes = 1 << 20
	l.SessionBytes = 2 << 20
	s := session(t, l)
	add(t, s, 1, 1<<20, 1<<20)
	add(t, s, 3, 1<<20, 1<<20)
	c, err := s.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Conn(3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Write(make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { _, e := c.Write([]byte{1}); results <- e }()
	go func() { _, e := c.Read(make([]byte, 1)); results <- e }()
	if err = c.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case e := <-results:
			if !errors.Is(e, transport.ErrClosed) {
				t.Fatal(e)
			}
		case <-time.After(time.Second):
			t.Fatal("stream close failed to unblock IO")
		}
	}
	if err = s.Receive(incoming(transport.Data, 3, 0, []byte("alive"))); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 5)
	if n, e := other.Read(b); e != nil || n != 5 || string(b) != "alive" {
		t.Fatal("unrelated flow was lost", n, e)
	}
	if _, err = other.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
}
func TestReadDeadlineCanBeUpdatedAndClearedWithoutKillingSession(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	c, err := s.Conn(1)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, e := c.Read(make([]byte, 1)); result <- e }()
	c.SetReadDeadline(time.Now().Add(-time.Millisecond))
	select {
	case e := <-result:
		if !errors.Is(e, os.ErrDeadlineExceeded) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("updated read deadline was lost")
	}
	c.SetReadDeadline(time.Time{})
	if err = s.Receive(incoming(transport.Data, 1, 0, []byte{5})); err != nil {
		t.Fatal(err)
	}
	if n, e := c.Read(make([]byte, 1)); e != nil || n != 1 {
		t.Fatal("deadline permanently killed stream", n, e)
	}
}
func TestFINCannotOvertakeUndeliveredBulkBytes(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	if err := s.Receive(incoming(transport.Finish, 1, 6, nil)); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Take(transport.Fast, transport.MaxPayload); ok {
		t.Fatal("FIN acknowledged before final byte")
	}
	if err := s.Receive(incoming(transport.Data, 1, 3, []byte("def"))); err != nil {
		t.Fatal(err)
	}
	if n, e := s.Read(1, make([]byte, 6)); n != 0 || e != nil {
		t.Fatal("out-of-order tail became premature EOF", n, e)
	}
	if err := s.Receive(incoming(transport.Data, 1, 0, []byte("abc"))); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	if n, e := s.Read(1, b); e != nil || n != 6 || string(b) != "abcdef" {
		t.Fatal(n, e)
	}
	if _, e := s.Read(1, b); !errors.Is(e, io.EOF) {
		t.Fatal(e)
	}
}
func exchange(t *testing.T, a, b *transport.Session) {
	t.Helper()
	for rounds := 0; rounds < 32; rounds++ {
		progress := false
		for _, pair := range [][2]*transport.Session{{a, b}, {b, a}} {
			for _, plane := range []transport.Plane{transport.Fast, transport.Bulk} {
				d, ok := pair[0].Take(plane, transport.MaxPayload)
				if !ok {
					continue
				}
				progress = true
				if err := pair[1].Receive(d.Record); err != nil {
					t.Fatal(err)
				}
				complete(t, d, transport.Accepted)
			}
		}
		if !progress {
			return
		}
	}
	t.Fatal("control exchange did not quiesce")
}
func TestThousandsOfShortConnectionsRetireWithoutReusingIDsOrLeakingSlots(t *testing.T) {
	l := transport.DefaultLimits()
	l.Streams = 2
	l.StreamBytes = 1 << 20
	l.SessionBytes = 2 << 20
	a := session(t, l)
	b := session(t, l)
	for i := 0; i < 2000; i++ {
		id := uint64(i*2 + 1)
		add(t, a, id, 1<<20, 1<<20)
		add(t, b, id, 1<<20, 1<<20)
		ac, e := a.Conn(id)
		if e != nil {
			t.Fatal(e)
		}
		bc, e := b.Conn(id)
		if e != nil {
			t.Fatal(e)
		}
		ac.CloseWrite()
		bc.CloseWrite()
		exchange(t, a, b)
		if _, e = ac.Read(make([]byte, 1)); !errors.Is(e, io.EOF) {
			t.Fatal(e)
		}
		if _, e = bc.Read(make([]byte, 1)); !errors.Is(e, io.EOF) {
			t.Fatal(e)
		}
		ac.Close()
		bc.Close()
	}
	if err := a.Add(1, 1<<20, 1<<20); !errors.Is(err, transport.ErrProtocol) {
		t.Fatal("retired OPEN replay allocated a new stream", err)
	}
}

func TestUncertainBulkCannotConsumeReservedProtocolDispatchSlots(t *testing.T) {
	l := transport.DefaultLimits()
	l.StreamBytes = 16 << 20
	s := session(t, l)
	add(t, s, 1, 16<<20, 16<<20)
	if e := s.Queue(1, make([]byte, 16<<20), time.Now()); e != nil {
		t.Fatal(e)
	}
	count := 0
	for {
		d, ok := s.TakeFor(transport.Bulk, transport.MaxPayload, transport.DataLedger)
		if !ok {
			break
		}
		complete(t, d, transport.Uncertain)
		count++
		if count > 256 {
			t.Fatal("unbounded uncertain dispatches")
		}
	}
	if count < 1 || count >= 256 {
		t.Fatal("protocol capacity was not protected", count)
	}
	if e := s.Receive(incoming(transport.Data, 1, 0, []byte("small peer request"))); e != nil {
		t.Fatal(e)
	}
	d, ok := s.TakeFor(transport.Fast, transport.MaxPayload, transport.ControlLedger)
	if !ok || d.Record.Kind != transport.Receipt {
		t.Fatal("bulk uncertainty blocked protocol progress")
	}
	complete(t, d, transport.Accepted)
}
func TestSmallReplyCarriesCumulativeReceiptAndCreditWithoutAnotherPhysicalControl(t *testing.T) {
	a := session(t, transport.DefaultLimits())
	b := session(t, transport.DefaultLimits())
	add(t, a, 1, 1<<20, 1<<20)
	add(t, b, 1, 1<<20, 1<<20)
	if e := a.Queue(1, []byte("request"), time.Now()); e != nil {
		t.Fatal(e)
	}
	request := take(t, a, transport.Fast, transport.MaxPayload)
	if e := b.Receive(request.Record); e != nil {
		t.Fatal(e)
	}
	complete(t, request, transport.Accepted)
	incoming := make([]byte, 7)
	if n, e := b.Read(1, incoming); e != nil || n != 7 {
		t.Fatal(n, e)
	}
	if e := b.Queue(1, []byte("reply"), time.Now()); e != nil {
		t.Fatal(e)
	}
	reply, ok := b.TakeFor(transport.Fast, transport.MaxPayload, transport.DataLedger)
	if !ok {
		t.Fatal("missing short reply")
	}
	if reply.Record.Acknowledged != 7 || reply.Record.ReceiveLimit != 1<<20+7 {
		t.Fatal("reply lacks peer progress", reply.Record)
	}
	if e := a.Receive(reply.Record); e != nil {
		t.Fatal(e)
	}
	complete(t, reply, transport.Accepted)
	if a.Snapshot().SendBuffered != 0 {
		t.Fatal("reply receipt did not release request replay bytes")
	}
	if b.ReadyFor(transport.Fast, transport.ControlLedger) {
		t.Fatal("coalesced progress still required a separate physical control")
	}
}

func TestContinuousLightRequestsDoNotBecomePermanentlyBulk(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	now := time.Now().Add(time.Hour)
	var sent uint64
	for i := 0; i < 10000; i++ {
		now = now.Add(100 * time.Millisecond)
		if e := s.Queue(1, make([]byte, 32), now); e != nil {
			t.Fatal(e)
		}
		d, ok := s.TakeFor(transport.Fast, transport.MaxPayload, transport.DataLedger)
		if !ok {
			t.Fatalf("light request %d became bulk despite 320 B/s offered load", i)
		}
		sent += uint64(len(d.Record.Payload))
		complete(t, d, transport.Accepted)
		if e := s.Receive(incoming(transport.Receipt, 1, sent, nil)); e != nil {
			t.Fatal(e)
		}
	}
}
func TestAggregateFastBudgetCannotBeMultipliedByOpeningMoreFlows(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	now := time.Now().Add(time.Hour)
	size := transport.DefaultLimits().InteractiveBytes
	for _, id := range []uint64{1, 3} {
		add(t, s, id, 1<<20, 1<<20)
		if e := s.Queue(id, make([]byte, size), now); e != nil {
			t.Fatal(e)
		}
	}
	first, ok := s.TakeFor(transport.Fast, transport.MaxPayload, transport.DataLedger)
	if !ok {
		t.Fatal("initial short burst unavailable")
	}
	complete(t, first, transport.Accepted)
	if _, ok = s.TakeFor(transport.Fast, transport.MaxPayload, transport.DataLedger); ok {
		t.Fatal("new flow multiplied aggregate fast allowance")
	}
	if _, ok = s.TakeFor(transport.Bulk, transport.MaxPayload, transport.DataLedger); !ok {
		t.Fatal("excess work waited for token refill instead of using bulk")
	}
}
