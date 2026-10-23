package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"testing"
	"time"
)

// A capped connection topology must not put live control receive behind the
// account-wide maintenance queue. Maintenance can be waiting for another socket
// or user; it does not own the otherwise idle control connection in this case.
func TestDEV8CappedReceiveKeepsItsOwnAdmissionClass(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.reader != nil {
		_ = m.reader.Close()
		m.reader = nil
	}
	if m.cleaner != nil {
		_ = m.cleaner.Close()
		m.cleaner = nil
	}
	m.dialGate = &accountDialGate{}
	release, err := m.dialGate.work.acquire(ctx, workMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	raw, _ := mailbox.Encode([]byte("OPEN_OK remains readable during maintenance"))
	if err := m.controlClient().Append(ctx, "down", raw); err != nil {
		t.Fatal(err)
	}
	readCtx, readCancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer readCancel()
	rows, _, err := m.Page(readCtx, "down", mailbox.Cursor{})
	if err != nil || len(rows) != 1 || string(rows[0].MIME) != string(raw) {
		t.Fatalf("idle control socket stranded behind maintenance: rows=%d err=%v", len(rows), err)
	}
}

// Account isolation must not turn one shared socket into four round-robin
// classes (urgent/read/cleanup/DATA) instead of the historical three. The
// latter silently gives DATA less service despite an unchanged writer count.
func TestDEV8CappedReceiveRetainsSharedSocketServiceShare(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cm, _ := tlsMailPair(t, ctx, []string{"up", "down"})
	m := cm.(*laneMail)
	if err := m.Activate(ctx, "down"); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.reader != nil {
		_ = m.reader.Close()
		m.reader = nil
	}
	if m.cleaner != nil {
		_ = m.cleaner.Close()
		m.cleaner = nil
	}
	m.dialGate = &accountDialGate{}
	m.baseWork.mu.Lock()
	m.baseWork.capacity = 1
	m.baseWork.mu.Unlock()
	release, err := m.baseWork.acquire(ctx, workData)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	done := make(chan error, 1)
	go func() { _, _, err := m.Page(ctx, "down", mailbox.Cursor{}); done <- err }()
	for {
		m.baseWork.mu.Lock()
		maintenance, receive := len(m.baseWork.queues[workMaintenance]), len(m.baseWork.queues[workReceive])
		m.baseWork.mu.Unlock()
		if maintenance+receive > 0 {
			m.dialGate.work.mu.Lock()
			accountReceive := m.dialGate.work.busy[workReceive]
			m.dialGate.work.mu.Unlock()
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if maintenance != 1 || receive != 0 || accountReceive != 1 {
				t.Fatalf("receive changed physical DATA share: socket maintenance=%d receive=%d account receive=%d", maintenance, receive, accountReceive)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("receive did not reach socket queue")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
