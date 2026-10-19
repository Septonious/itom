package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"sync"
	"testing"
	"time"
)

// Closure during the gap between speedtests must heal without new DATA.
func TestIdleClientDATAClosureRecoversBeforeNextPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cm, sm := tlsMailPair(t, ctx, []string{"up", "down"})
	defer sm.(*laneMail).Close()
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	base, reader, cleaner := m.Client, m.readerRef(), m.cleaner
	m.writerMu.Lock()
	old := append([]*mailbox.Client(nil), m.writers...)
	m.writerMu.Unlock()
	for _, c := range old {
		_ = c.Close()
	}
	if m.DataAvailable() {
		t.Error("closed sockets still advertised DATA capacity")
	}
	fresh := func() bool {
		m.writerMu.Lock()
		defer m.writerMu.Unlock()
		return len(m.writers) == 2 && m.writers[0] != old[0] && m.writers[1] != old[1]
	}
	deadline := time.Now().Add(4 * time.Second)
	for !fresh() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !fresh() {
		t.Fatal("idle DATA did not heal without a new APPEND")
	}
	if m.Client != base || m.readerRef() != reader || m.cleaner != cleaner {
		t.Fatal("DATA repair replaced independent roles")
	}
	raw, _ := mailbox.Encode([]byte("next load uses repaired sockets"))
	if err := m.AppendData(ctx, "up", raw); err != nil {
		t.Fatal(err)
	}
	rows, _, err := sm.Page(ctx, "up", mailbox.Cursor{})
	if err != nil || len(rows) != 1 {
		t.Fatal("payload after idle repair", len(rows), err)
	}
}

func TestIdleSharedDATAClosureRecoversWithoutPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cm, sm := tlsMailPair(t, ctx, []string{"data"})
	defer cm.(*laneMail).Close()
	defer sm.(*laneMail).Close()
	seed := cm.(*laneMail)
	var mu sync.Mutex
	var opened []*mailbox.Client
	r := newAccountRuntime(ctx, []string{"u"}, func(ctx context.Context) (appendClient, error) {
		c, e := seed.dial(ctx)
		if e == nil {
			mu.Lock()
			opened = append(opened, c)
			mu.Unlock()
		}
		return c, e
	})
	defer r.Close()
	raw, _ := mailbox.Encode([]byte("prime both physical workers"))
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- r.Append(ctx, "u", "data", raw) }()
	}
	for range 2 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	mu.Lock()
	old := append([]*mailbox.Client(nil), opened...)
	mu.Unlock()
	if len(old) != 2 {
		t.Fatal("fixture did not prime both workers", len(old))
	}
	for _, c := range old {
		_ = c.Close()
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(opened)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	n := len(opened)
	mu.Unlock()
	if n < 4 {
		t.Fatal("shared DATA did not heal during idle", n)
	}
	if s := r.snapshot(); s.Completed != 2 || s.Failed != 0 || s.PendingBytes != 0 {
		t.Fatal("repair invented a publication", s)
	}
}
