package mailstream

import (
	"context"
	"io"
	"testing"
)

func TestV160IntegratedReassemblyMetrics(t *testing.T) {
	sess := &Session{ctx: context.Background()}
	a, b := newStream(sess, 1), newStream(sess, 2)
	defer a.cancel()
	defer b.cancel()
	if e := a.accept(frame{kind: frameData, stream: 1, offset: 4, data: []byte("efgh")}); e != nil {
		t.Fatal(e)
	}
	if e := b.accept(frame{kind: frameData, stream: 2, data: []byte("ok")}); e != nil {
		t.Fatal(e)
	}
	if sess.gapBuffered.Load() != 4 || sess.readyBuffered.Load() != 2 || sess.receiveBuffered.Load() != 6 {
		t.Fatal("incorrect receive accounting")
	}
	p := make([]byte, 8)
	if n, e := b.Read(p); n != 2 || e != nil || string(p[:n]) != "ok" {
		t.Fatal(n, e)
	}
	if e := a.accept(frame{kind: frameData, stream: 1, offset: 0, data: []byte("abcdef")}); e != nil {
		t.Fatal(e)
	}
	if sess.gapBuffered.Load() != 0 || sess.readyBuffered.Load() != 8 || sess.gapResolved.Load() != 1 || sess.duplicatePayload.Load() != 2 {
		t.Fatal("gap did not resolve")
	}
	if e := a.accept(frame{kind: frameFIN, stream: 1, offset: 8}); e != nil {
		t.Fatal(e)
	}
	if n, e := a.Read(p); n != 8 || e != nil || string(p) != "abcdefgh" {
		t.Fatal(n, e, string(p))
	}
	if n, e := a.Read(p); n != 0 || e != io.EOF {
		t.Fatal(n, e)
	}
	if sess.receiveBuffered.Load() != 0 || sess.readyBuffered.Load() != 0 || sess.consumedPayload.Load() != 10 || sess.receivedPayload.Load() != 10 {
		t.Fatal("consumption mismatch")
	}
}
