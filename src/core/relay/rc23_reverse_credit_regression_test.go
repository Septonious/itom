package relay

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// The peer delivers data but temporarily stops reading the reverse direction.
// Once payload is buffered, a local Read must not wait for a WINDOW write.
// net.Pipe is deliberately more restrictive than a buffered network socket;
// this isolates the dependency, not the frequency of occurrence in the field.
func TestRC23ReadProgressIndependentOfReverseCreditWrite(t *testing.T) {
	local, peer := net.Pipe()
	c := newCreditConn(local)
	defer c.Close()
	defer peer.Close()
	frame := make([]byte, 5+relayFrameBytes)
	frame[0] = relayData
	binary.BigEndian.PutUint32(frame[1:5], uint32(relayFrameBytes))
	for i := 5; i < len(frame); i++ {
		frame[i] = byte(i)
	}
	_ = peer.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Write(frame); err != nil {
		t.Fatal(err)
	}
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	buf := make([]byte, relayFrameBytes)
	_ = c.SetReadDeadline(time.Now().Add(time.Second))
	go func() { n, err := io.ReadFull(c, buf); done <- result{n, err} }()
	select {
	case r := <-done:
		if r.n != len(buf) || r.err != nil {
			t.Fatalf("read: %d %v", r.n, r.err)
		}
		for i, v := range buf {
			if v != frame[5+i] {
				t.Fatalf("corrupt byte %d", i)
			}
		}
	case <-time.After(1500 * time.Millisecond):
		// Close releases the underlying net.Pipe write; never leave goroutines behind.
		_ = c.Close()
		<-done
		t.Fatal("buffered data read blocked beyond read deadline by reverse WINDOW write")
	}
}
