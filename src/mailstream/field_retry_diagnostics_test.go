package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestFieldRetryDiagnosticsClassifyActualDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	link := &dispatchIsolationLink{admitted: make(chan byte, 16), rejected: make(chan struct{}, 1)}
	old := time.Now().Add(-4 * time.Second)
	s := &Session{ctx: ctx, cancel: cancel, link: link, wake: make(chan struct{}, 1), changed: make(chan struct{}), pending: map[uint64]*pendingPacket{
		1: {kind: frameWindow, wire: []byte{frameWindow}, priority: true, attempts: 1, sent: old},
		2: {kind: frameData, wire: []byte{frameData}, attempts: 1, sent: old},
	}}
	done := make(chan struct{})
	go func() { defer close(done); s.dispatchLoop() }()
	defer func() { cancel(); <-done }()
	for _, want := range []byte{frameWindow, frameData} {
		select {
		case got := <-link.admitted:
			if got != want {
				t.Fatalf("got %d want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("retry not dispatched")
		}
	}
	deadline := time.Now().Add(time.Second)
	for {
		snap := s.Snapshot()
		if snap.Retransmissions == 2 {
			if snap.RetransmitKinds[frameWindow] != 1 || snap.RetransmitKinds[frameData] != 1 {
				t.Fatalf("wrong frame counts: %+v", snap.RetransmitKinds)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("retry accounting: %+v", snap)
		}
		time.Sleep(time.Millisecond)
	}
}
