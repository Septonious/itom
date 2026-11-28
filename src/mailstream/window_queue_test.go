package mailstream

import (
	"context"
	"testing"
	"time"
)

func TestV160WindowQueueLatestCredit(t *testing.T) {
	q := newWindowQueue()
	q.offer(1, 100, 256)
	id, w, ok := q.take()
	if !ok || id != 1 || w.consumed != 100 {
		t.Fatal(id, w, ok)
	}
	q.offer(1, 200, 512)
	q.offer(1, 150, 256)
	q.complete(id, w)
	_, w, ok = q.take()
	if !ok || w.consumed != 200 || w.limit != 512 {
		t.Fatal(w, ok)
	}
	q.complete(1, w)
	if _, _, ok = q.take(); ok {
		t.Fatal("stale update retained")
	}
}
func TestV160StreamReadDoesNotWaitForWindowPublisher(t *testing.T) {
	sess := &Session{ctx: context.Background(), windows: newWindowQueue()}
	s := newStream(sess, 1)
	defer s.cancel()
	if e := s.accept(frame{kind: frameData, stream: 1, data: make([]byte, 64<<10)}); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := s.Read(make([]byte, 64<<10)); done <- e }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("read blocked by absent window worker")
	}
	id, w, ok := sess.windows.take()
	if !ok || id != 1 || w.consumed != 64<<10 || w.limit != streamWindow {
		t.Fatal(id, w, ok)
	}
}
