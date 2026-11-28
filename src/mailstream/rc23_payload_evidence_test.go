package mailstream

import (
	"context"
	"testing"
)

// APPEND, PING and receipt traffic cannot make a dormant user look busy.
// A duplicate accepted DATA frame must not inflate incoming payload evidence.
func TestRC23PayloadEvidenceCountsUniqueData(t *testing.T) {
	s := &Session{ctx: context.Background()}
	stream := newStream(s, 1)
	defer stream.cancel()
	data := frame{kind: frameData, stream: 1, data: []byte("abcdef")}
	if err := stream.accept(data); err != nil {
		t.Fatal(err)
	}
	if err := stream.accept(data); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().ReceivedPayload; got != 6 {
		t.Fatalf("duplicate counted: %d", got)
	}
	data.offset = 6
	if err := stream.accept(data); err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot().ReceivedPayload; got != 12 {
		t.Fatalf("new DATA not counted: %d", got)
	}
	data.offset = 8
	if err := stream.accept(data); err == nil {
		t.Fatal("overlapping DATA accepted")
	}
	if got := s.Snapshot().ReceivedPayload; got != 12 {
		t.Fatalf("rejected DATA counted: %d", got)
	}
}
