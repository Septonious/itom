package wire

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type vector struct{ Header, Frame, Payload string }

func checkIndependentWireVector(t *testing.T) {
	b, err := os.ReadFile("testdata/wire2.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vector
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	hb, err := hex.DecodeString(v.Header)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := hex.DecodeString(v.Frame)
	if err != nil {
		t.Fatal(err)
	}
	h, err := ParseHeader(hb)
	if err != nil {
		t.Fatal(err)
	}
	if h.Kind != Record || h.Pair[0] != 1 || h.Session[0] != 17 || h.Epoch != 7 || h.Counter != 9 || h.BodyLength != 115 {
		t.Fatal(h)
	}
	encoded, err := h.MarshalBinary()
	if err != nil || !bytes.Equal(hb, encoded) {
		t.Fatal(err)
	}
	f, err := ParseFrame(fb)
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != Data || f.Stream != 1 || f.Logical != 3 || f.Offset != 0 || string(f.Payload) != "abc" {
		t.Fatal(f)
	}
	encoded, err = f.MarshalBinary()
	if err != nil || !bytes.Equal(fb, encoded) {
		t.Fatal(err)
	}
}
func checkEnvelopeBoundsBeforeBodyRead(t *testing.T) {
	h := Header{Kind: Record, Pair: ID{1}, Session: ID{2}, Epoch: 1, BodyLength: MaxBody}
	b, err := h.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int{6, 7, 45, 46, 47, 60, 61, 62, 63} {
		bad := append([]byte(nil), b...)
		bad[offset] = 1
		if _, err = ParseHeader(bad); err == nil {
			t.Fatal("accepted reserved byte", offset)
		}
	}
	for i := 56; i < 60; i++ {
		b[i] = 255
	}
	if _, body, err := ReadEnvelope(bytes.NewReader(b)); err == nil || body != nil {
		t.Fatal("unbounded body", err)
	}
}
func checkFrameRejectsInvalidShapes(t *testing.T) {
	for _, f := range []Frame{
		{Type: Data, Stream: 1, Logical: 1},
		{Type: Data, Stream: 2, Logical: 1, Payload: []byte{1}},
		{Type: Data, Stream: 1, Logical: 1, Offset: MaxOffset, Payload: []byte{1}},
		{Type: Data, Stream: 1, Logical: 1, Payload: make([]byte, MaxData+1)},
		{Type: Ping, Stream: 1}, {Type: Type(255)},
	} {
		if _, err := f.MarshalBinary(); err == nil {
			t.Fatal("invalid frame accepted", f.Type)
		}
	}
}
func FuzzParseFrame(f *testing.F) {
	seed, _ := (Frame{Type: Data, Stream: 1, Logical: 1, Payload: []byte("abc")}).MarshalBinary()
	f.Add(seed)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxRecord {
			return
		}
		frame, err := ParseFrame(b)
		if err != nil {
			return
		}
		out, err := frame.MarshalBinary()
		if err != nil || !bytes.Equal(out, b) {
			t.Fatal("accepted noncanonical frame", err)
		}
	})
}
func FuzzParseHeader(f *testing.F) {
	seed, _ := (Header{Kind: Record, Pair: ID{1}, Session: ID{2}, Epoch: 1, BodyLength: 112}).MarshalBinary()
	f.Add(seed)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := ParseHeader(b)
		if err != nil {
			return
		}
		out, err := h.MarshalBinary()
		if err != nil || !bytes.Equal(b, out) {
			t.Fatal("accepted noncanonical header", err)
		}
	})
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioWire(t *testing.T) {
	t.Log("check=checkIndependentWireVector")
	checkIndependentWireVector(t)
	t.Log("check=checkEnvelopeBoundsBeforeBodyRead")
	checkEnvelopeBoundsBeforeBodyRead(t)
	t.Log("check=checkFrameRejectsInvalidShapes")
	checkFrameRejectsInvalidShapes(t)
}
