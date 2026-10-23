package tunnel

import (
	"context"
	"itom.local/core/mailbox"
	"itom.local/core/servercfg"
	"testing"
	"time"
)

func TestDEV9ShortDataBorrowsReaderAndJoinsIdleWithoutDuplicateAppend(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "free-reader", true: "idle-reader"}[idle], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cm, sm := tlsMailPair(t, ctx, []string{"up", "down", "up-data-00", "down-data-00"})
			m := cm.(*laneMail)
			defer m.Close()
			defer sm.(*laneMail).Close()
			m.topology = servercfg.Transport{Layout: "split"}
			caps, err := m.stageTopology(ctx)
			if err != nil {
				t.Fatal(err)
			}
			m.topologyCaps = caps
			reader := m.dataReaderRef()
			if _, _, err := reader.PageTailLimit(ctx, "down-data-00", mailbox.Cursor{}, true, 1); err != nil {
				t.Fatal(err)
			}
			release, err := m.sharedSocketForTest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			waited := make(chan error, 1)
			if idle {
				go func() { _, _, e := m.waitData(ctx, "down-data-00", 2*time.Second); waited <- e }()
				for {
					m.dataWork.mu.Lock()
					busy := m.dataWork.busy[workReceive]
					m.dataWork.mu.Unlock()
					if busy > 0 {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("IDLE admission missing")
					default:
						time.Sleep(time.Millisecond)
					}
				}
			}
			raw, _ := mailbox.Encode([]byte("one durable short BODY"))
			writeCtx, stop := context.WithTimeout(ctx, 500*time.Millisecond)
			defer stop()
			if err := m.AppendDataPriority(writeCtx, "up-data-00", raw, true); err != nil {
				t.Fatal("short publication queued behind unrelated control maintenance", err)
			}
			if idle {
				if err := <-waited; err != nil {
					t.Fatal("IDLE did not join", err)
				}
			}
			if !reader.Ready() {
				t.Fatal("wake closed authenticated socket")
			}
			rows, _, err := sm.(*laneMail).controlClient().Page(ctx, "up-data-00", mailbox.Cursor{})
			if err != nil || len(rows) != 1 || string(rows[0].MIME) != string(raw) {
				t.Fatalf("publication was duplicated/lost: rows=%d err=%v", len(rows), err)
			}
			m.borrowHintMu.Lock()
			hints := len(m.borrowHints)
			m.borrowHintMu.Unlock()
			if hints != 1 {
				t.Fatal("borrowed APPENDUID discovery missing", hints)
			}
		})
	}
}
func (m *laneMail) sharedSocketForTest(ctx context.Context) (func(), error) {
	m.baseWork.mu.Lock()
	m.baseWork.capacity = 1
	m.baseWork.mu.Unlock()
	return m.baseWork.acquire(ctx, workMaintenance)
}

func TestDEV9AdmissionCancellationReturnsEverySocketPermit(t *testing.T) {
	m := &laneMail{}
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		if i%2 == 0 {
			cancel()
		}
		lease, err := m.shortSocket(ctx)
		cancel()
		if err == nil {
			lease.release()
		}
	}
	for _, b := range []*accountWorkBudget{&m.baseWork, &m.dataWork} {
		b.mu.Lock()
		for i := range b.busy {
			if b.busy[i] != 0 || len(b.queues[i]) != 0 {
				t.Errorf("socket permit leaked class=%d busy=%d queued=%d", i, b.busy[i], len(b.queues[i]))
			}
		}
		b.mu.Unlock()
	}
}
