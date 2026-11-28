package v160

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
	"time"
)

func TestV160ReassemblyAndRepair(t *testing.T) {
	var r Reassembler
	now := time.Unix(1, 0)
	used := 0
	reserve := func(n int) bool { used += n; return true }
	if n, e := r.Add(4, []byte("efgh"), 100, now, reserve); n != 4 || e != nil {
		t.Fatal(n, e)
	}
	s := r.Stats()
	if s.Blocked != 4 || s.Ready != 0 || s.GapOffset != 0 || s.GapEnd != 4 || s.GapSince != now {
		t.Fatal(s)
	}
	if _, e := r.Add(6, []byte("ghij"), 100, now, reserve); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Add(3, []byte("dX"), 100, now, reserve); !errors.Is(e, ErrProtocol) {
		t.Fatal("conflicting overlap", e)
	}
	if used != 6 {
		t.Fatal("conflict charged", used)
	}
	if e := r.SetFinal(10, 100, now); e != nil {
		t.Fatal(e)
	}
	if n, e := r.Read(make([]byte, 10)); n != 0 || e != nil {
		t.Fatal("gap delivered", n, e)
	}
	if n, e := r.Add(0, []byte("abcdef"), 100, now.Add(time.Millisecond), reserve); n != 4 || e != nil {
		t.Fatal(n, e)
	}
	s = r.Stats()
	if s.Blocked != 0 || s.Ready != 10 || !s.GapSince.IsZero() {
		t.Fatal(s)
	}
	buf := make([]byte, 10)
	if n, e := r.Read(buf); n != 10 || e != nil || string(buf) != "abcdefghij" {
		t.Fatal(n, e, string(buf))
	}
	if n, e := r.Read(buf); n != 0 || !errors.Is(e, io.EOF) {
		t.Fatal(n, e)
	}
	if n, e := r.Add(0, []byte("abcdefghij"), 100, now, reserve); n != 0 || e != nil {
		t.Fatal(n, e)
	}
	if used != 10 {
		t.Fatal(used)
	}
}
func TestV160ReassemblyRandomOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for repeat := 0; repeat < 50; repeat++ {
		var r Reassembler
		data := make([]byte, 4096)
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		order := rng.Perm(128)
		for _, i := range order {
			at := i * 32
			if _, e := r.Add(uint64(at), data[at:at+32], 8192, time.Now(), nil); e != nil {
				t.Fatal(e)
			}
			if i%5 == 0 {
				if _, e := r.Add(uint64(at), data[at:at+32], 8192, time.Now(), nil); e != nil {
					t.Fatal(e)
				}
			}
		}
		got := make([]byte, len(data))
		if n, e := r.Read(got); n != len(got) || e != nil || !bytes.Equal(data, got) {
			t.Fatal(n, e)
		}
		s := r.Stats()
		if s.Buffered != 0 || s.Ready != 0 || s.Blocked != 0 || s.Ranges != 0 {
			t.Fatal(s)
		}
	}
}
func TestV160ReassemblyCapacityAtomic(t *testing.T) {
	var r Reassembler
	now := time.Now()
	_, e := r.Add(4, []byte("ef"), 8, now, nil)
	if e != nil {
		t.Fatal(e)
	}
	before := r.Stats()
	if _, e = r.Add(0, []byte("abcd"), 8, now, func(int) bool { return false }); !errors.Is(e, ErrCapacity) {
		t.Fatal(e)
	}
	if r.Stats() != before {
		t.Fatal("failed reserve mutated stream")
	}
	if _, e = r.Add(8, []byte("a"), 8, now, nil); !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
	if e = r.SetFinal(5, 8, time.Now()); !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
}

func TestV160ReassemblyDetectsMissingTailFromFIN(t *testing.T) {
	var r Reassembler
	now := time.Now()
	if e := r.SetFinal(5, 100, now); e != nil {
		t.Fatal(e)
	}
	s := r.Stats()
	if s.GapOffset != 0 || s.GapEnd != 5 || s.GapSince != now || s.Blocked != 0 {
		t.Fatal(s)
	}
	if _, e := r.Add(0, []byte("hello"), 100, now.Add(time.Millisecond), nil); e != nil {
		t.Fatal(e)
	}
	if !r.Stats().GapSince.IsZero() {
		t.Fatal("tail gap remained")
	}
}
