package secure

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sync"
	"testing"

	"itom.local/core/wire"
)

func binding() Binding {
	return Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: wire.ID{3},
		Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
}
func pair(t *testing.T) (*Epoch, *Epoch) {
	t.Helper()
	psk := bytes.Repeat([]byte{17}, 32)
	a, err := NewHandshake(binding(), psk, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewHandshake(binding(), psk, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	copyFirst, err := a.Write(nil)
	if err != nil || !bytes.Equal(first, copyFirst) {
		t.Fatal("non-idempotent handshake")
	}
	if err = b.Read(first); err != nil {
		t.Fatal(err)
	}
	if err = b.Read(first); err != nil {
		t.Fatal(err)
	}
	second, err := b.Write(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Read(second); err != nil {
		t.Fatal(err)
	}
	if err = a.Read(second); err != nil {
		t.Fatal(err)
	}
	left, err := a.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	right, err := b.Epoch()
	if err != nil {
		t.Fatal(err)
	}
	return left, right
}
func checkBidirectionalNoiseAndReordering(t *testing.T) {
	a, b := pair(t)
	for _, endpoints := range [][2]*Epoch{{a, b}, {b, a}} {
		var packets [][]byte
		for i := 0; i < 4; i++ {
			p, err := endpoints[0].Seal(wire.Frame{Type: wire.Data, Stream: 1, Logical: uint64(i + 1), Offset: uint64(i), Payload: []byte{byte(i)}})
			if err != nil {
				t.Fatal(err)
			}
			packets = append(packets, p)
		}
		for _, i := range []int{3, 1, 0, 2} {
			f, err := endpoints[1].Open(packets[i])
			if err != nil || !bytes.Equal(f.Payload, []byte{byte(i)}) {
				t.Fatalf("direction/order %d: %v", i, err)
			}
			if _, err = endpoints[1].Open(packets[i]); !errors.Is(err, ErrReplay) {
				t.Fatalf("duplicate: %v", err)
			}
		}
	}
}
func checkOuterTamperCannotPoisonReplay(t *testing.T) {
	a, b := pair(t)
	p, err := a.Seal(wire.Frame{Type: wire.Ping})
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), p...)
	binary.BigEndian.PutUint64(bad[48:56], 100000)
	if _, err = b.Open(bad); !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
	if _, err = b.Open(p); err != nil {
		t.Fatal("invalid packet advanced replay window", err)
	}
}
func checkWrongUserAndWrongPSK(t *testing.T) {
	for _, badPSK := range []bool{false, true} {
		psk := bytes.Repeat([]byte{17}, 32)
		other := append([]byte(nil), psk...)
		bnd := binding()
		if badPSK {
			other[0] ^= 1
		} else {
			bnd.User = "12345679"
		}
		a, err := NewHandshake(binding(), psk, true)
		if err != nil {
			t.Fatal(err)
		}
		b, err := NewHandshake(bnd, other, false)
		if err != nil {
			t.Fatal(err)
		}
		msg, err := a.Write(nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = b.Read(msg); err == nil {
			t.Fatal("accepted different binding or PSK")
		}
		if _, err = b.Epoch(); err == nil {
			t.Fatal("failed handshake yielded keys")
		}
	}
}
func checkFreshSessionRejectsOldPacket(t *testing.T) {
	a, _ := pair(t)
	_, fresh := pair(t)
	p, err := a.Seal(wire.Frame{Type: wire.Ping})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.Open(p); err == nil {
		t.Fatal("fresh ephemeral keys accepted old ciphertext")
	}
}
func checkRecordSizeAndHeadroom(t *testing.T) {
	a, b := pair(t)
	p, err := a.Seal(wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: make([]byte, wire.MaxData)})
	if err != nil || len(p) != 32944 {
		t.Fatalf("size=%d err=%v", len(p), err)
	}
	if _, err = b.Open(p); err != nil {
		t.Fatal(err)
	}
	a.txBytes = HardBytes - ControlHeadroom
	if _, err = a.Seal(wire.Frame{Type: wire.Data, Stream: 1, Logical: 2, Payload: []byte{1}}); !errors.Is(err, ErrKeyLimit) {
		t.Fatal(err)
	}
	if _, err = a.Seal(wire.Frame{Type: wire.Ping}); err != nil {
		t.Fatal(err)
	}
	a.txBytes = HardBytes + 1
	if _, err = a.Seal(wire.Frame{Type: wire.Ping}); !errors.Is(err, ErrKeyLimit) {
		t.Fatal("overflow", err)
	}
}
func TestConcurrentSealUsesUniqueNonces(t *testing.T) {
	a, b := pair(t)
	const n = 128
	packets := make(chan []byte, n)
	failures := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := a.Seal(wire.Frame{Type: wire.Ping})
			if err != nil {
				failures <- err
			} else {
				packets <- p
			}
		}()
	}
	wg.Wait()
	close(packets)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	count := 0
	for p := range packets {
		if _, err := b.Open(p); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if count != n {
		t.Fatal(count)
	}
}
func checkReplayWindowWrapAndAge(t *testing.T) {
	var w replayWindow
	for _, n := range []uint64{0, 5, 65535, 65536, 131072} {
		if err := w.check(n); err != nil {
			t.Fatal(err)
		}
		w.commit(n)
		if w.check(n) == nil {
			t.Fatal("duplicate", n)
		}
	}
	if w.check(65536) == nil {
		t.Fatal("accepted aged counter")
	}
	if err := w.check(131071); err != nil {
		t.Fatal(err)
	}
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioSecure(t *testing.T) {
	t.Log("check=checkBidirectionalNoiseAndReordering")
	checkBidirectionalNoiseAndReordering(t)
	t.Log("check=checkOuterTamperCannotPoisonReplay")
	checkOuterTamperCannotPoisonReplay(t)
	t.Log("check=checkWrongUserAndWrongPSK")
	checkWrongUserAndWrongPSK(t)
	t.Log("check=checkFreshSessionRejectsOldPacket")
	checkFreshSessionRejectsOldPacket(t)
	t.Log("check=checkRecordSizeAndHeadroom")
	checkRecordSizeAndHeadroom(t)
	t.Log("check=checkReplayWindowWrapAndAge")
	checkReplayWindowWrapAndAge(t)
}
