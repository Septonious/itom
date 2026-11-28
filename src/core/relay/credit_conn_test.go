package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"itom.local/mailstream"
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
func TestRelayRejectLegacyALPN(t *testing.T) {
	d, _ := testExit(t)
	d.tls = d.tls.Clone()
	d.tls.NextProtos = []string{"olcrtc-relay/1"}
	c, err := d.Connect(context.Background(), testTarget(t))
	if err == nil {
		c.Close()
		t.Fatal("legacy ALPN negotiated")
	}
}
func TestRelayTruncatedFIN(t *testing.T) {
	a, b := net.Pipe()
	receiver := newCreditConn(a)
	defer receiver.Close()
	_ = b.Close()
	if _, err := io.ReadAll(receiver); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

func TestV160RelayCreditOnEveryBearer(t *testing.T) {
	d, _ := testExit(t)
	for _, mobile := range []bool{false, true, false} {
		ctx := mailstream.WithCellularExit(context.Background(), mobile)
		c, err := d.Connect(ctx, testTarget(t))
		if err != nil {
			t.Fatal(err)
		}
		_, credit := c.(*creditConn)
		if !credit {
			c.Close()
			t.Fatalf("mobile=%t credit=%t", mobile, credit)
		}
		c.Close()
	}
}
