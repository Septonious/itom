package tunnel

import (
	"context"
	"itom.local/core/wire"
	"sync"
	"testing"
	"time"
)

// Requests can race across striped stream owners. A shorter response window
// must not cancel the longer window already installed on the shared reader.
func TestReaderConcurrentRequestsKeepExistingResponseWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &laneMail{}
	l := &lane{ctx: ctx, mail: m, readWake: make(chan struct{}, 1), controlQueue: make(chan queuedControl, 32), urgentQueue: make(chan queuedControl, 32), receiptQueue: make(chan queuedControl, 32)}
	until := time.Now().Add(2 * time.Second).UnixNano()
	l.readHotUntil.Store(until)
	m.readerHotUntil.Store(until)
	f, err := wire.EncodeControl(1, wire.Connect{Host: "example.invalid", Port: 443})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.SendControl(ctx, f); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if l.readHotUntil.Load() != until || m.readerHotUntil.Load() != until {
		t.Fatal("existing response window shortened")
	}
	if len(l.readWake) != 1 {
		t.Fatal("request wake lost")
	}
}
