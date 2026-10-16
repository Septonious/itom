package mailbox

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSocketCloseHooksRunOnceIncludingLateRegistration(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c := &Client{conn: &cappedConn{Conn: left}}
	var calls atomic.Int32
	c.OnClose(func() { calls.Add(1) })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.conn.Close() }()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("close released admission multiple times")
	}
	c.OnClose(func() { calls.Add(1) })
	if calls.Load() != 2 {
		t.Fatal("closed-before-registration leaked admission")
	}
}
