package mailstream

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The control path can reject a publication while the independent
// interactive writer still has room.
type dispatchIsolationLink struct {
	blocked  atomic.Bool
	admitted chan byte
	rejected chan struct{}
}

func (l *dispatchIsolationLink) admit(kind byte, control bool) error {
	if control && l.blocked.Load() {
		select {
		case l.rejected <- struct{}{}:
		default:
		}
		return ErrCapacity
	}
	l.admitted <- kind
	return nil
}
func (l *dispatchIsolationLink) Send(_ context.Context, wire []byte, priority bool) error {
	return l.admit(wire[0], priority)
}
func (l *dispatchIsolationLink) SendHousekeeping(_ context.Context, wire []byte) error {
	return l.admit(wire[0], true)
}
func (l *dispatchIsolationLink) SendFeedback(_ context.Context, wire []byte) error {
	return l.admit(wire[0], true)
}
func (l *dispatchIsolationLink) SendInteractive(_ context.Context, wire []byte) error {
	return l.admit(wire[0], false)
}
func (l *dispatchIsolationLink) Receive() <-chan []byte                  { return nil }
func (l *dispatchIsolationLink) Attach(context.Context, SessionID) error { return nil }
func (l *dispatchIsolationLink) Close() error                            { return nil }

func TestDispatchIndependentInteractiveAdmission(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind byte
	}{
		{"housekeeping", frameControlRead}, {"feedback", frameWindow}, {"ordinary-control", frameReset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			link := &dispatchIsolationLink{admitted: make(chan byte, 16), rejected: make(chan struct{}, 1)}
			link.blocked.Store(true)
			s := &Session{ctx: ctx, cancel: cancel, link: link, wake: make(chan struct{}, 1), changed: make(chan struct{}), pending: map[uint64]*pendingPacket{
				1: {kind: tc.kind, wire: []byte{tc.kind}, priority: true},
				2: {kind: framePing, wire: []byte{framePing}, priority: true},
				3: {kind: frameData, wire: []byte{frameData}},
			}}
			done := make(chan struct{})
			go func() { defer close(done); s.dispatchLoop() }()
			defer func() { cancel(); <-done }()
			select {
			case <-link.rejected:
			case <-time.After(time.Second):
				t.Fatal("fixture did not reject the earlier control packet")
			}
			select {
			case kind := <-link.admitted:
				if kind != framePing {
					t.Fatalf("independent ping must precede blocked control and bulk: %d", kind)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("independent interactive writer starved by control backpressure")
			}
			select {
			case kind := <-link.admitted:
				t.Fatalf("unexpected packet %d while control blocked", kind)
			case <-time.After(15 * time.Millisecond):
			}
			link.blocked.Store(false)
			for _, want := range []byte{tc.kind, frameData} {
				select {
				case kind := <-link.admitted:
					if kind != want {
						t.Fatalf("recovery order: got %d, want %d", kind, want)
					}
				case <-time.After(time.Second):
					t.Fatalf("blocked packet %d did not recover", want)
				}
			}
		})
	}
}
