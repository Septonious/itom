package datapath

import (
	"context"
	"io"
	"itom.local/core/wire"
	"net"
	"testing"
	"time"
)

func tcpFixture(t *testing.T, private bool) (*TCPExit, *net.TCPListener, wire.Frame) {
	t.Helper()
	l, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	x, err := NewTCPExit(context.Background(), ExitConfig{Generation: 1, MaxSockets: 2, MaxDials: 1, Metadata: 4, AllowPrivate: private})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(x.Stop)
	return x, l, control(t, 1, wire.Connect{Host: "127.0.0.1", Port: uint16(l.Addr().(*net.TCPAddr).Port)})
}
func TestDuplicateDialCannotTerminalizeOriginalOrCloseAdoptedSocket(t *testing.T) {
	x, l, f := tcpFixture(t, true)
	a, err := x.Request(0, f)
	if err != nil {
		t.Fatal(err)
	}
	r := x.Dial(*a.Dial)
	duplicate := x.Dial(*a.Dial)
	if _, _, err = x.Complete(time.Millisecond, duplicate); err == nil {
		t.Fatal("duplicate dial completed original OPEN")
	}
	_, c, err := x.Complete(2*time.Millisecond, r)
	if err != nil || c == nil {
		t.Fatal("original not adopted", err)
	}
	if _, _, err = x.Complete(3*time.Millisecond, r); err == nil {
		t.Fatal("duplicate completion accepted")
	}
	l.SetDeadline(time.Now().Add(time.Second))
	peer, err := l.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(time.Second))
	if n, err := c.Write([]byte("x")); n != 1 || err != nil {
		t.Fatal("adopted socket was closed", n, err)
	}
	b := make([]byte, 1)
	if _, err = io.ReadFull(peer, b); err != nil || b[0] != 'x' {
		t.Fatal(err)
	}
	if err = x.Retire(1); err != nil {
		t.Fatal(err)
	}
	a, err = x.Request(4*time.Millisecond, f)
	if err != nil || a.Dial != nil || a.Reply == nil {
		t.Fatal("retired OPEN redialled", a, err)
	}
}
func TestStoppedExitClosesUnadoptedSuccess(t *testing.T) {
	x, l, f := tcpFixture(t, true)
	a, err := x.Request(0, f)
	if err != nil {
		t.Fatal(err)
	}
	r := x.Dial(*a.Dial)
	if r.conn == nil {
		t.Fatal("dial failed")
	}
	l.SetDeadline(time.Now().Add(time.Second))
	peer, err := l.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	x.Stop()
	if _, c, _ := x.Complete(time.Millisecond, r); c != nil {
		t.Fatal("adopted after stop")
	}
	peer.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 1)
	if n, err := peer.Read(b); n != 0 || err != io.EOF {
		t.Fatal("unadopted socket leaked", n, err)
	}
}
func TestDefaultExitRejectsLoopbackBeforeDial(t *testing.T) {
	x, _, f := tcpFixture(t, false)
	a, err := x.Request(0, f)
	if err != nil {
		t.Fatal(err)
	}
	r := x.Dial(*a.Dial)
	if r.conn != nil || r.reason != wire.ReasonPolicy {
		t.Fatal("loopback allowed", r.reason)
	}
	a, c, err := x.Complete(time.Millisecond, r)
	if err != nil || c != nil || a.Reply == nil {
		t.Fatal(a, err)
	}
	m, err := wire.DecodeControl(*a.Reply)
	if err != nil || m.(wire.ConnectResult).Code != wire.ReasonPolicy {
		t.Fatal(m, err)
	}
}
