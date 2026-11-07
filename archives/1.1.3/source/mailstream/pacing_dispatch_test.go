package mailstream

import (
	"context"
	"testing"
	"time"
)

type publicationProbe struct {
	bytes    int
	priority bool
	at       time.Time
}
type pacingProbeLink struct {
	sent chan publicationProbe
	in   chan []byte
}

func (l *pacingProbeLink) Send(_ context.Context, body []byte, priority bool) error {
	l.sent <- publicationProbe{len(body), priority, time.Now()}
	return nil
}
func (l *pacingProbeLink) Receive() <-chan []byte                  { return l.in }
func (l *pacingProbeLink) Attach(context.Context, SessionID) error { return nil }
func (l *pacingProbeLink) Close() error                            { return nil }

// A previously measured large flight must not be published all at once into a
// slow access link. Control must still pass while bulk waits for its next burst.
func TestMeasuredBulkBurstLeavesInteractivePublicationImmediate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := make([]byte, 32)
	h, hello, err := initiate(key, "pacing")
	if err != nil {
		t.Fatal(err)
	}
	_, reply, err := respond(key, "pacing", hello)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := h.finish(reply)
	if err != nil {
		t.Fatal(err)
	}
	link := &pacingProbeLink{sent: make(chan publicationProbe, 64), in: make(chan []byte)}
	s := newSession(ctx, link, cipher, false, nil)
	defer s.Close()
	s.mu.Lock()
	s.delivery.limit = 8 << 20
	s.delivery.rate = 512 << 10
	s.delivery.base = 200 * time.Millisecond
	s.delivery.saturatedBase = 300 * time.Millisecond
	s.delivery.last = time.Now()
	s.delivery.controlBase = 200 * time.Millisecond
	s.delivery.observeInteractive(900*time.Millisecond, time.Now().Add(-100*time.Millisecond))
	s.delivery.observeInteractive(900*time.Millisecond, time.Now())
	s.mu.Unlock()
	for range 20 {
		if err = s.trySend(frame{kind: frameData, stream: 1, data: make([]byte, 32<<10)}, false); err != nil {
			t.Fatal(err)
		}
	}
	first := <-link.sent
	if first.priority {
		t.Fatal("bulk fixture did not publish DATA")
	}
	ping := time.Now()
	if err = s.trySend(frame{kind: framePing, offset: 1}, true); err != nil {
		t.Fatal(err)
	}
	bulk := first.bytes
	control := false
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case p := <-link.sent:
			if p.priority {
				control = true
				if p.at.Sub(ping) > 75*time.Millisecond {
					t.Fatal("interactive packet waited behind bulk pacing")
				}
			} else {
				bulk += p.bytes
			}
		case <-timer.C:
			if !control {
				t.Fatal("interactive packet starved behind paced DATA")
			}
			if bulk > maxMailPlain {
				t.Fatalf("burst published %d bytes into a measured slow path", bulk)
			}
			return
		}
	}
}
