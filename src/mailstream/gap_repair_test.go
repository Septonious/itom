package mailstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"sync"
	"testing"
	"time"
)

// The first DATA record is accepted into a slow path, but never visible to
// the peer. Its repair must be the original ciphertext on an alternate path.
type repairTestLink struct {
	bearerTestLink
	mu      sync.Mutex
	hold    bool
	held    []byte
	repairs int
}

func (l *repairTestLink) Send(ctx context.Context, b []byte, priority bool) error {
	if !priority && l.hold {
		l.mu.Lock()
		if l.held == nil {
			l.held = bytes.Clone(b)
			l.mu.Unlock()
			return nil
		}
		l.mu.Unlock()
	}
	return l.bearerTestLink.Send(ctx, b, priority)
}
func (l *repairTestLink) SendRepair(ctx context.Context, b []byte) error {
	l.mu.Lock()
	if !bytes.Equal(l.held, b) {
		l.mu.Unlock()
		return ErrProtocol
	}
	l.repairs++
	l.mu.Unlock()
	return l.bearerTestLink.Send(ctx, b, false)
}
func TestV160SelectiveGapRepair(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	key := make([]byte, 32)
	h, hello, e := initiate(key, "gap-test")
	if e != nil {
		t.Fatal(e)
	}
	sc, reply, e := respond(key, "gap-test", hello)
	if e != nil {
		t.Fatal(e)
	}
	cc, e := h.finish(reply)
	if e != nil {
		t.Fatal(e)
	}
	ready, _ := (frame{kind: frameReady}).encode()
	ack, _ := (frame{kind: frameReadyAck}).encode()
	if _, _, e = cc.seal(ready); e != nil {
		t.Fatal(e)
	}
	if _, _, e = sc.seal(ack); e != nil {
		t.Fatal(e)
	}
	cl, sl := &repairTestLink{hold: true}, &repairTestLink{}
	client := newSession(ctx, cl, cc, false, nil)
	defer client.Close()
	server := newSession(ctx, sl, sc, true, nil)
	defer server.Close()
	cl.peer.Store(server)
	sl.peer.Store(client)
	st := newStream(server, 1)
	st.markOpen()
	server.mu.Lock()
	server.streams[1] = st
	server.mu.Unlock()
	a, b := bytes.Repeat([]byte{0x11}, 8192), bytes.Repeat([]byte{0x33}, 8192)
	if e = client.send(ctx, frame{kind: frameData, stream: 1, data: a}, false); e != nil {
		t.Fatal(e)
	}
	if e = client.send(ctx, frame{kind: frameData, stream: 1, offset: uint64(len(a)), data: b}, false); e != nil {
		t.Fatal(e)
	}
	// Bulk normal timeout is at least 3s. Recovery must finish well before it.
	st.SetReadDeadline(time.Now().Add(1800 * time.Millisecond))
	got := make([]byte, len(a)+len(b))
	if _, e = io.ReadFull(st, got); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got, append(a, b...)) {
		t.Fatal("payload corruption")
	}
	cs, ss := client.Snapshot(), server.Snapshot()
	if cs.RepairAdmitted != 1 || ss.GapReportsSent == 0 || ss.GapResolved != 1 || ss.GapBuffered != 0 || cs.RepairWireBytes == 0 {
		t.Fatalf("repair not accounted: client=%+v server=%+v", cs, ss)
	}
	cl.mu.Lock()
	repairs := cl.repairs
	held := bytes.Clone(cl.held)
	cl.mu.Unlock()
	if repairs != 1 {
		t.Fatal(repairs)
	}
	// The delayed original can arrive after its repair without a second apply.
	if e = cl.bearerTestLink.Send(ctx, held, false); e != nil {
		t.Fatal(e)
	}
	if e = client.send(ctx, frame{kind: frameFIN, stream: 1, offset: uint64(len(got))}, true); e != nil {
		t.Fatal(e)
	}
	if n, e := st.Read(make([]byte, 1)); n != 0 || e != io.EOF {
		t.Fatal(n, e)
	}
	if server.Snapshot().ReceivedPayload != uint64(len(got)) {
		t.Fatal("duplicate credited")
	}
}
func TestV160GapRequestBoundedAndValidated(t *testing.T) {
	now := time.Now().Add(-time.Second)
	s := &Session{changed: make(chan struct{}), wake: make(chan struct{}, 1), pending: map[uint64]*pendingPacket{1: {kind: frameData, stream: 1, offset: 0, payloadBytes: 8192, attempts: 1, sent: now, wire: make([]byte, 8192)}, 2: {kind: frameData, stream: 1, offset: 8192, payloadBytes: 8192}, 3: {kind: frameData, stream: 3, offset: 0, payloadBytes: 8192, attempts: 1, sent: now}, 4: {kind: frameData, stream: 1, offset: 16384, payloadBytes: 8192, attempts: 1, sent: now, earlyRepaired: true}}}
	f := frame{kind: frameGap, stream: 1, offset: 0, data: binary.BigEndian.AppendUint64(nil, 32768)}
	for range 3 {
		if e := s.acceptGap(f); e != nil {
			t.Fatal(e)
		}
	}
	if s.repairRequests != 1 || !s.pending[1].repairRequested || s.pending[2].repairRequested || s.pending[3].repairRequested || s.pending[4].repairRequested {
		t.Fatal("gap request admitted unrelated or unsent work")
	}
	if s.canRepairLocked(s.pending[1]) {
		t.Fatal("repair without budget")
	}
	s.repairCredit = 8192
	if !s.canRepairLocked(s.pending[1]) {
		t.Fatal("eligible repair rejected")
	}
	f.data = binary.BigEndian.AppendUint64(nil, maxGapReportBytes+1)
	if e := s.acceptGap(f); e != ErrProtocol {
		t.Fatal("invalid gap accepted", e)
	}
}
