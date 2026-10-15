package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"itom.local/core/socks"
	"itom.local/core/wire"
)

// A simultaneous socket burst is different from a few already-open bulk
// streams. Keep every successful OPEN live before issuing first-byte traffic,
// then verify both small exchanges and larger records, half-close, and reuse.
// This is a protocol scenario, not an emulation of the Ookla application/TUN.
func TestManyTCPConcurrentOpenExchangeAndFINKeepsCarriers(t *testing.T) {
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "25")
	t.Setenv("ITOM_LOAD_VISIBILITY_DELAY_MS", "100")
	t.Setenv("ITOM_LOAD_PROVIDER_CONNECTION_LIMIT", "10")
	const count = 96
	f := newProductionSupervisorFixtureAccounts(t, 5)
	snapshots := snapshotLoadCarriers(t, f, 5, 5)
	target := wire.Connect{Host: "127.0.0.1", Port: uint16(f.listener.Addr().(*net.TCPAddr).Port)}
	ctx, cancel := context.WithTimeout(f.ctx, 45*time.Second)
	defer cancel()
	type opened struct {
		stream socks.Stream
		err    error
	}
	results := make(chan opened, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; s, _, err := f.client.Open(ctx, target); results <- opened{s, err} }()
	}
	close(start)
	wg.Wait()
	close(results)
	var streams []socks.Stream
	for r := range results {
		if r.err != nil {
			t.Errorf("concurrent OPEN: %v", r.err)
		} else {
			streams = append(streams, r.stream)
		}
	}
	defer func() {
		for _, s := range streams {
			s.Close()
		}
	}()
	if len(streams) != count {
		t.Fatalf("opened=%d/%d unexpected_exits=%d details=%v", len(streams), count, f.carrierExits.unexpected.Load(), f.carrierExits.failureDetails())
	}
	exchangeStart := make(chan struct{})
	errors := make(chan error, count)
	for i, s := range streams {
		wg.Add(1)
		go func(i int, s socks.Stream) {
			defer wg.Done()
			<-exchangeStart
			conn := s.(net.Conn)
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			for step := 0; step < 4; step++ {
				size := 32
				if step == 2 {
					size = 32 << 10
				}
				payload := bytes.Repeat([]byte{byte(i), byte(step), 0x6d, 0x33}, size/4)
				if _, err := s.Write(payload); err != nil {
					errors <- fmt.Errorf("write %d/%d: %w", i, step, err)
					return
				}
				got := make([]byte, len(payload))
				if _, err := io.ReadFull(s, got); err != nil {
					errors <- fmt.Errorf("read %d/%d: %w", i, step, err)
					return
				}
				if !bytes.Equal(got, payload) {
					errors <- fmt.Errorf("identity %d/%d", i, step)
					return
				}
			}
			if err := s.CloseWrite(); err != nil {
				errors <- err
				return
			}
			got, err := io.ReadAll(io.LimitReader(s, 1))
			if err != nil || len(got) != 0 {
				errors <- fmt.Errorf("FIN %d: n=%d err=%v", i, len(got), err)
			}
		}(i, s)
	}
	close(exchangeStart)
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	for _, s := range streams {
		s.Close()
	}
	streams = nil
	s, _, err := f.client.Open(ctx, target)
	if err != nil {
		t.Fatal("OPEN after burst", err)
	}
	defer s.Close()
	s.(net.Conn).SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = s.Write([]byte("after-burst")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("after-burst"))
	if _, err = io.ReadFull(s, got); err != nil || string(got) != "after-burst" {
		t.Fatal("post burst", string(got), err)
	}
	assertLoadCarriersPreserved(t, f, snapshots)
	t.Logf("simultaneous_open=%d exchanges_each=4 verified_bytes_each=%d original_carriers_preserved=true app_emulated=false", count, 32*3+(32<<10))
}
