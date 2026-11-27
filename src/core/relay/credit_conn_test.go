package relay

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestRelayConsumedCreditBoundsUnreadData(t *testing.T) {
	a, b := net.Pipe()
	sender, receiver := newCreditConn(a), newCreditConn(b)
	defer sender.Close()
	defer receiver.Close()
	data := bytes.Repeat([]byte{0x15}, relayCreditWindow+relayFrameBytes)
	_ = sender.SetWriteDeadline(time.Now().Add(150 * time.Millisecond))
	n, err := sender.Write(data)
	if n != relayCreditWindow || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("unread credit escaped: %d %v", n, err)
	}
	_ = sender.SetWriteDeadline(time.Time{})
	got := make([]byte, relayCreditWindow)
	if _, err = io.ReadFull(receiver, got); err != nil || !bytes.Equal(got, data[:n]) {
		t.Fatal("credit read", err)
	}
	if _, err = sender.Write(data[n:]); err != nil {
		t.Fatal(err)
	}
	if err = sender.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(receiver)
	if err != nil || !bytes.Equal(rest, data[n:]) {
		t.Fatal("final data", err)
	}
	// FIN closes only the data direction, never the reverse window/control.
	if _, err = receiver.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err = receiver.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(sender)
	if err != nil || string(reply) != "ok" {
		t.Fatal("reverse direction", err)
	}
}

func TestRelayLegacyALPNStillWorks(t *testing.T) {
	d, _ := testExit(t)
	d.tls = d.tls.Clone()
	d.tls.NextProtos = []string{relayProtocolV1}
	c, err := d.Connect(context.Background(), testTarget(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if raw, ok := c.(*tls.Conn); !ok || raw.ConnectionState().NegotiatedProtocol != relayProtocolV1 {
		t.Fatal("legacy connection changed framing")
	}
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = c.Write([]byte("legacy")); err != nil {
		t.Fatal(err)
	}
	if err = c.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	reply, err := io.ReadAll(c)
	if err != nil || len(reply) != 64 {
		t.Fatal("legacy transfer", err)
	}
}
