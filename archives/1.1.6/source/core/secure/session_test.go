package secure_test

import (
	"bytes"
	"itom.local/core/secure"
	"itom.local/core/wire"
	"sync"
	"testing"
)

func epochs(t *testing.T) (*secure.Epoch, *secure.Epoch) {
	t.Helper()
	binding := secure.Binding{Server: wire.ID{1}, User: "12345678", Pair: wire.ID{2}, Session: wire.ID{3}, Handshake: wire.ID{4}, Epoch: 1, ConfigHash: [32]byte{5}}
	a, e := secure.NewHandshake(binding, bytes.Repeat([]byte{6}, 32), true)
	if e != nil {
		t.Fatal(e)
	}
	b, e := secure.NewHandshake(binding, bytes.Repeat([]byte{6}, 32), false)
	if e != nil {
		t.Fatal(e)
	}
	first, e := a.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = b.Read(first); e != nil {
		t.Fatal(e)
	}
	second, e := b.Write(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Read(second); e != nil {
		t.Fatal(e)
	}
	x, e := a.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	y, e := b.Epoch()
	if e != nil {
		t.Fatal(e)
	}
	return x, y
}
func TestConcurrentPublishersCannotReuseANonceOrAcceptReplay(t *testing.T) {
	sender, receiver := epochs(t)
	var wg sync.WaitGroup
	packets := make(chan []byte, 64)
	for i := range 64 {
		wg.Go(func() {
			p, e := sender.Seal(wire.Frame{Type: wire.Data, Stream: 1, Logical: uint64(i + 1), Payload: []byte("verified payload")})
			if e != nil {
				t.Error(e)
				return
			}
			packets <- p
		})
	}
	wg.Wait()
	close(packets)
	seen := map[string]bool{}
	count := 0
	for p := range packets {
		if seen[string(p)] {
			t.Fatal("ciphertext/nonce reused")
		}
		seen[string(p)] = true
		if _, e := receiver.Open(p); e != nil {
			t.Fatal(e)
		}
		if _, e := receiver.Open(p); e == nil {
			t.Fatal("replay delivered twice")
		}
		count++
	}
	if count != 64 {
		t.Fatal(count)
	}
}
func TestTamperedPacketDoesNotPoisonAuthenticDelivery(t *testing.T) {
	sender, receiver := epochs(t)
	p, e := sender.Seal(wire.Frame{Type: wire.Data, Stream: 1, Logical: 1, Payload: []byte("body")})
	if e != nil {
		t.Fatal(e)
	}
	bad := append([]byte(nil), p...)
	bad[len(bad)-1] ^= 1
	if _, e = receiver.Open(bad); e == nil {
		t.Fatal("tamper accepted")
	}
	if _, e = receiver.Open(p); e != nil {
		t.Fatal("tamper consumed authentic nonce", e)
	}
}
