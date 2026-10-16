package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"itom.local/core/wire"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Persistent small TCP exchanges after actual IMAP IDLE, followed by FIN.
// Protocol integrity scenario; this does not emulate a particular speed-test app
// or replace the fixed-window application performance qualification.
func TestShortTCPRequestResponseAfterIdlePreservesBytesAndFIN(t *testing.T) {
	t.Setenv("ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", "10")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "25")
	t.Setenv("ITOM_LOAD_VISIBILITY_DELAY_MS", "100")
	f := newProductionSupervisorFixtureAccounts(t, 5)
	snapshots := snapshotLoadCarriers(t, f, 5, 5)
	_, p, err := net.SplitHostPort(f.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(f.ctx, 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	failures := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stream, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(port)})
			if err != nil {
				failures <- err
				return
			}
			defer stream.Close()
			conn := stream.(net.Conn)
			conn.SetDeadline(time.Now().Add(15 * time.Second))
			for step := 0; step < 7; step++ {
				if step == 3 {
					if err = pause(ctx, 2200*time.Millisecond); err != nil {
						failures <- err
						return
					}
				}
				payload := []byte(fmt.Sprintf("short-request-%d-%d-%s", i, step, bytes.Repeat([]byte{'x'}, step+1)))
				if _, err = stream.Write(payload); err != nil {
					failures <- err
					return
				}
				got := make([]byte, len(payload))
				if _, err = io.ReadFull(stream, got); err != nil {
					failures <- err
					return
				}
				if !bytes.Equal(got, payload) {
					failures <- fmt.Errorf("short TCP byte identity %d/%d", i, step)
					return
				}
			}
			if err = stream.CloseWrite(); err != nil {
				failures <- err
				return
			}
			got, err := io.ReadAll(io.LimitReader(stream, 1))
			if err != nil || len(got) != 0 {
				failures <- fmt.Errorf("short TCP FIN: bytes=%d err=%v", len(got), err)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	assertLoadCarriersPreserved(t, f, snapshots)
}
