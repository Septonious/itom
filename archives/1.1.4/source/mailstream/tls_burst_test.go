package mailstream

import (
	"context"
	"testing"
	"time"
)

// TLS dynamic record sizing can emit an entire initial body as small writes.
// The byte burst must become bulk even though no individual write exceeds 4 KiB.
// Independent requests and paced small messages must retain immediate treatment.
func TestSmallTLSRecordBurstDoesNotConsumeInteractiveReserve(t *testing.T) {
	key := make([]byte, 32)
	h, hello, err := initiate(key, "record-burst")
	if err != nil {
		t.Fatal(err)
	}
	_, reply, err := respond(key, "record-burst", hello)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := h.finish(reply)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{ctx: context.Background(), cipher: cipher, pending: map[uint64]*pendingPacket{}, closedStreams: map[uint32]*terminalStream{}, changed: make(chan struct{}), wake: make(chan struct{}, 1)}
	burst := newStream(s, 1)
	for range 24 {
		if _, err = burst.Write(make([]byte, 1200)); err != nil {
			t.Fatal(err)
		}
	}
	interactive, bulk := 0, 0
	for _, p := range s.pending {
		if p.priority {
			interactive++
		} else {
			bulk++
		}
	}
	if interactive > 4 || bulk == 0 {
		t.Errorf("TLS-sized burst flooded interactive publication: immediate=%d bulk=%d", interactive, bulk)
	}
	other := newStream(s, 3)
	if _, err = other.Write([]byte("independent request")); err != nil {
		t.Fatal(err)
	}
	paced := newStream(s, 5)
	for range 12 {
		if _, err = paced.Write(make([]byte, 512)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(40 * time.Millisecond)
	}
	for _, p := range s.pending {
		if (p.stream == 3 || p.stream == 5) && !p.priority {
			t.Fatal("isolated or paced small request lost immediate treatment")
		}
	}
}
