package transport_test

import (
	"bytes"
	"errors"
	"io"
	"itom.local/core/transport"
	"testing"
	"time"
)

func session(t *testing.T, limits transport.Limits) *transport.Session {
	t.Helper()
	s, e := transport.NewSession(7, limits)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func add(t *testing.T, s *transport.Session, id, credit, window uint64) {
	t.Helper()
	if e := s.Add(id, credit, window); e != nil {
		t.Fatal(e)
	}
}
func TestSmallRequestIsImmediatelyEligibleWhileBulkIsQueued(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 8<<20, 8<<20)
	add(t, s, 2, 8<<20, 8<<20)
	now := time.Now()
	if e := s.Queue(1, make([]byte, 1<<20), now); e != nil {
		t.Fatal(e)
	}
	if e := s.Queue(2, []byte("first TLS record"), now); e != nil {
		t.Fatal(e)
	}
	r, ok := next(t, s, transport.Fast, 64<<10)
	if !ok || r.Stream != 2 || string(r.Payload) != "first TLS record" {
		t.Fatalf("interactive request was delayed: %+v", r)
	}
	r, ok = next(t, s, transport.Bulk, 64<<10)
	if !ok || r.Stream != 1 || len(r.Payload) != 64<<10 {
		t.Fatalf("bulk service lost: %+v", r)
	}
}
func TestFragmentedBulkCannotKeepFastPriorityButLaterRequestCan(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 8<<20, 8<<20)
	// Symbolic policy time keeps this classification assertion independent
	// of the host CPU schedule. Application/TCP deadlines use real clocks.
	now := time.Now().Add(time.Hour)
	for i := 0; i < transport.DefaultLimits().InteractiveBytes/1024; i++ {
		if e := s.Queue(1, make([]byte, 1024), now); e != nil {
			t.Fatal(e)
		}
		if _, ok := next(t, s, transport.Fast, 1024); !ok {
			t.Fatal("short initial burst blocked")
		}
	}
	if e := s.Queue(1, make([]byte, 1024), now); e != nil {
		t.Fatal(e)
	}
	if _, ok := next(t, s, transport.Fast, 1024); ok {
		t.Fatal("fragmented bulk monopolized fast")
	}
	if _, ok := next(t, s, transport.Bulk, 1024); !ok {
		t.Fatal("bulk not eligible")
	}
	if e := s.Queue(1, []byte("new request"), now.Add(2*time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, ok := next(t, s, transport.Fast, 1024); !ok {
		t.Fatal("established flow permanently stuck as bulk")
	}
}
func TestReorderedDeliveryAndHalfClosePreserveEveryByte(t *testing.T) {
	s := session(t, transport.DefaultLimits())
	add(t, s, 1, 1<<20, 1<<20)
	record := func(k transport.Kind, offset uint64, p string) transport.Record {
		return transport.Record{Kind: k, Generation: 7, Stream: 1, Offset: offset, Payload: []byte(p)}
	}
	for _, r := range []transport.Record{record(transport.Data, 3, "def"), record(transport.Finish, 6, ""), record(transport.Data, 0, "abc")} {
		if e := s.Receive(r); e != nil {
			t.Fatal(e)
		}
	}
	b := make([]byte, 6)
	n, e := s.Read(1, b)
	if e != nil || n != 6 || string(b) != "abcdef" {
		t.Fatalf("delivery %q: %v", b, e)
	}
	if _, e = s.Read(1, b); e != io.EOF {
		t.Fatal("half close lost", e)
	}
	r, ok := next(t, s, transport.Fast, 64)
	if !ok || r.Kind != transport.FinishAck || r.Offset != 6 {
		t.Fatal("final cumulative acknowledgement missing", r)
	}
	if _, ok := next(t, s, transport.Fast, 64); ok {
		t.Fatal("finished input generated redundant receipt or new credit")
	}
}
func TestAdvertisedCreditHasActualMemoryBacking(t *testing.T) {
	l := transport.DefaultLimits()
	l.SessionBytes = 8 << 20
	s := session(t, l)
	add(t, s, 1, 1<<20, 5<<20)
	if e := s.Add(2, 1<<20, 4<<20); !errors.Is(e, transport.ErrCapacity) {
		t.Fatal("overpromised receive storage", e)
	}
	add(t, s, 2, 1<<20, 3<<20)
	if e := s.GrowReceive(1, 6<<20); !errors.Is(e, transport.ErrCapacity) {
		t.Fatal("window growth overcommitted memory", e)
	}
}
func TestInvalidOverlapAndPrematureFINCannotTruncateAFlow(t *testing.T) {
	for _, bad := range []transport.Record{{Kind: transport.Data, Offset: 2, Payload: []byte("overlap")}, {Kind: transport.Finish, Offset: 5}} {
		s := session(t, transport.DefaultLimits())
		add(t, s, 1, 1<<20, 1<<20)
		if e := s.Receive(transport.Record{Kind: transport.Data, Generation: 7, Stream: 1, Offset: 3, Payload: []byte("def")}); e != nil {
			t.Fatal(e)
		}
		bad.Generation = 7
		bad.Stream = 1
		if e := s.Receive(bad); !errors.Is(e, transport.ErrProtocol) {
			t.Fatalf("accepted conflicting byte ownership: %v", e)
		}
	}
}
func TestServiceLearningDoesNotPunishLoadedRTTOrForgetCapacityAtIdle(t *testing.T) {
	var m transport.ServiceModel
	now := time.Now()
	m.Observe(now, 12500000, time.Second, 100*time.Millisecond, false)
	before := m.Window(1<<20, 8<<20)
	if before < 2500000 {
		t.Fatal("slow window acquisition", before)
	}
	m.Observe(now.Add(time.Second), 12500000, time.Second, 1900*time.Millisecond, false)
	if after := m.Window(1<<20, 8<<20); after != before {
		t.Fatal("loaded RTT changed propagation flight", before, after)
	}
	m.Observe(now.Add(time.Minute), 100, time.Second, 200*time.Millisecond, true)
	if after := m.Window(1<<20, 8<<20); after < before {
		t.Fatal("idle erased measured service")
	}
	if n := m.Batch(150, 200, time.Millisecond); n != 150 {
		t.Fatal("small body waits for fill", n)
	}
}
func FuzzRecordDecoderNeverAcceptsLengthOrScopeMismatch(f *testing.F) {
	seed, _ := (transport.Record{Kind: transport.Data, Generation: 7, Stream: 1, Payload: []byte("hello")}).MarshalBinary()
	f.Add(seed)
	f.Add([]byte{2})
	f.Fuzz(func(t *testing.T, b []byte) {
		r, e := transport.DecodeRecord(b)
		if e != nil {
			return
		}
		out, e := r.MarshalBinary()
		if e != nil || !bytes.Equal(b, out) {
			t.Fatal("noncanonical record accepted")
		}
	})
}

func TestServiceBatchCannotOverflowIntoOneByteAdmission(t *testing.T) {
	var m transport.ServiceModel
	m.Observe(time.Now(), 64<<20, time.Nanosecond, 100*time.Millisecond, false)
	if n := m.Batch(64<<10, 64<<10, time.Second); n != 64<<10 {
		t.Fatal("service conversion overflow throttled batch", n)
	}
}
func TestUnloadedPathChangeCanRefreshPropagationWithoutLosingBandwidth(t *testing.T) {
	var m transport.ServiceModel
	now := time.Now()
	m.Observe(now, 12500000, time.Second, 40*time.Millisecond, false)
	initial := m.Window(1<<20, 8<<20)
	m.Observe(now.Add(15*time.Second), 1024, time.Second, 180*time.Millisecond, true)
	if after := m.Window(1<<20, 8<<20); after <= initial {
		t.Fatal("old path minimum starved the new path", initial, after)
	}
}

// Unit-only handoff helper: no provider throughput or delivery is inferred.
func next(t *testing.T, s *transport.Session, plane transport.Plane, max int) (transport.Record, bool) {
	t.Helper()
	d, ok := s.Take(plane, max)
	if !ok {
		return transport.Record{}, false
	}
	r := d.Record
	if err := d.Complete(transport.Accepted); err != nil {
		t.Fatal(err)
	}
	return r, true
}
