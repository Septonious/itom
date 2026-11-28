package v160

import (
	"bytes"
	"reflect"
	"testing"
)

func pair(t *testing.T) (*RecordCipher, *RecordCipher) {
	t.Helper()
	key := Key{"abcd", 1}
	psk := bytes.Repeat([]byte{7}, 32)
	i, h, e := Begin(key, psk)
	if e != nil {
		t.Fatal(e)
	}
	s, r, e := Accept(key, psk, h)
	if e != nil {
		t.Fatal(e)
	}
	c, e := i.Finish(r)
	if e != nil {
		t.Fatal(e)
	}
	return c, s
}
func TestV160RecordRoundTrip(t *testing.T) {
	c, s := pair(t)
	frames := []Frame{{Kind: Data, Stream: 1, Offset: 10, Payload: []byte("abc")}, {Kind: Window, Stream: 2, Offset: 13, Value: 100}, {Kind: Ping, Offset: 123}}
	p, e := EncodeFrames(frames)
	if e != nil {
		t.Fatal(e)
	}
	w, seq, e := c.Seal(frames)
	if e != nil || seq != 0 || len(w) != len(p)+24 {
		t.Fatal(e, seq, len(w))
	}
	got, seq, e := s.Open(w)
	if e != nil || seq != 0 || !reflect.DeepEqual(frames, got) {
		t.Fatal(got, seq, e)
	}
	if _, _, e = c.Open(w); e == nil {
		t.Fatal("direction reflection accepted")
	}
	other, _ := pair(t)
	if _, _, e = other.Open(w); e == nil {
		t.Fatal("different handshake accepted")
	}
	w[len(w)-1] ^= 1
	if _, _, e = s.Open(w); e == nil {
		t.Fatal("tampered tag accepted")
	}
	w[len(w)-1] ^= 1
	b, e := Pack([][]byte{w, w})
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Unpack(b)
	if e != nil || len(rs) != 2 || !bytes.Equal(rs[1], w) {
		t.Fatal(e)
	}
}
func TestV160HandshakeContext(t *testing.T) {
	key := Key{"abcd", 1}
	psk := bytes.Repeat([]byte{1}, 32)
	_, h, e := Begin(key, psk)
	if e != nil {
		t.Fatal(e)
	}
	for _, other := range []Key{{"abcd", 2}, {"abce", 1}} {
		if _, _, e = Accept(other, psk, h); e == nil {
			t.Fatal("cross-session hello accepted")
		}
	}
	if _, _, e = Accept(key, bytes.Repeat([]byte{2}, 32), h); e == nil {
		t.Fatal("wrong PSK accepted")
	}
}
func TestV160ReplaySpan(t *testing.T) {
	c, s := pair(t)
	var r ReplayWindow
	for j := uint64(0); j < ReplaySpan; j++ {
		w, seq, e := c.Seal([]Frame{{Kind: Ping, Offset: j}})
		if e != nil || seq != j {
			t.Fatal(seq, e)
		}
		if j == 0 {
			continue
		}
		if _, seq, e = s.Open(w); e != nil {
			t.Fatal(e)
		}
		if e = r.Commit(seq); e != nil {
			t.Fatal(e)
		}
	}
	if dup, e := r.Check(0); dup || e != nil {
		t.Fatal("old missing record lost", dup, e)
	}
	if _, _, e := c.Seal([]Frame{{Kind: Ready}}); e == nil {
		t.Fatal("send span overflow")
	}
	if _, e := r.Check(ReplaySpan); e == nil {
		t.Fatal("receive span overflow")
	}
	if e := r.Commit(0); e != nil {
		t.Fatal(e)
	}
	if e := r.Advance(ReplaySpan); e != nil {
		t.Fatal(e)
	}
	if e := c.AdvanceSendFloor(ReplaySpan); e != nil {
		t.Fatal(e)
	}
	if dup, e := r.Check(0); !dup || e != nil {
		t.Fatal("retired duplicate not recognized")
	}
	w, seq, e := c.Seal([]Frame{{Kind: Ready}})
	if e != nil || seq != ReplaySpan {
		t.Fatal(seq, e)
	}
	if _, _, e = s.Open(w); e != nil {
		t.Fatal(e)
	}
}
func TestV160NoncanonicalFrames(t *testing.T) {
	for _, b := range [][]byte{{byte(Ping), 0x80, 0}, {byte(Data), 1, 0, 3, 1}, {byte(FIN), 0, 0}, {255}, {byte(OpenOK), 1, 255}, {byte(Window), 1, 2, 1}} {
		if _, e := DecodeFrames(b); e == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}
func FuzzV160Frames(f *testing.F) {
	f.Add([]byte{byte(Data), 1, 0, 1, 'a'})
	f.Fuzz(func(t *testing.T, b []byte) {
		fs, e := DecodeFrames(b)
		if e == nil {
			enc, e := EncodeFrames(fs)
			if e != nil || !bytes.Equal(enc, b) {
				t.Fatal("noncanonical")
			}
		}
	})
}
