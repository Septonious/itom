//go:build itomtest

package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"itom.local/core/journal"
	"itom.local/core/pool"
	"itom.local/core/servercfg"
	"itom.local/core/wire"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Independent download/upload streams and a persistent small request channel
// exercise the phase shape missing from a short, symmetric echo test. Measured
// journal cost is a service-time replay, not a server outage or a release gate.
func TestRC12RetainedPhasedTransfersWithObservedJournalCost(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	t.Setenv("ITOM_LOAD_CLIENT_RTT_MS", "10")
	t.Setenv("ITOM_LOAD_CLIENT_MBPS", "500")
	t.Setenv("ITOM_LOAD_SERVER_RTT_MS", "20")
	t.Setenv("ITOM_LOAD_SERVER_MBPS", "1000")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	t.Setenv("ITOM_LOAD_UID_HISTORY", "64")
	t.Setenv("ITOM_LOAD_DATA_SHARDS", "4")
	f := newProductionSupervisorFixtureState(t, 10, func(serverDir, _ string, cfg servercfg.Config, u servercfg.User) {
		for _, a := range cfg.Accounts {
			dir := filepath.Join(serverDir, a.ID, u.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			store, err := journal.Open(filepath.Join(dir, "data.db"), ledgerConfig(a, u, dataProfile12), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err = store.EnableAfterInventory(a.ID, u.ID, store.Run()); err != nil {
				t.Fatal(err)
			}
			if _, err = store.IssueRemote(journal.Scope{Account: a.ID, User: u.ID, Session: wire.ID{99}}, pool.Amount{Bytes: 15 << 19, Messages: 150}); err != nil {
				t.Fatal(err)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	f.mu.Lock()
	for _, l := range f.serverLanes {
		applyTestCommitDelay(l, 20*time.Millisecond)
	}
	f.mu.Unlock()
	t.Log("replay server durable-commit service=20ms plus real fsync; client route=10ms/500Mbps server route=20ms/1000Mbps; not field qualification")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var accepted []net.Conn
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range accepted {
			c.Close()
		}
	})
	const size = 1 << 20
	pattern := func(seed byte) []byte {
		b := make([]byte, size)
		for i := range b {
			b[i] = byte((i + int(seed)) % 251)
		}
		return b
	}
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, c)
			mu.Unlock()
			go func() {
				defer c.Close()
				var request [2]byte
				if _, err := io.ReadFull(c, request[:]); err != nil {
					return
				}
				switch request[0] {
				case 'D':
					productionWriteFull(c, pattern(request[1]))
				case 'U':
					got := make([]byte, size)
					if _, err := io.ReadFull(c, got); err == nil && bytes.Equal(got, pattern(request[1])) {
						productionWriteFull(c, []byte("OK"))
					}
				case 'C':
					io.Copy(c, c)
				}
			}()
		}
	}()
	port := uint16(listener.Addr().(*net.TCPAddr).Port)
	open := func() (net.Conn, error) {
		ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
		defer cancel()
		p, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: port})
		if err != nil {
			return nil, err
		}
		c, ok := p.(net.Conn)
		if !ok {
			p.Close()
			return nil, fmt.Errorf("missing deadline-capable stream")
		}
		return c, nil
	}
	chat, err := open()
	if err != nil {
		t.Fatal(err)
	}
	defer chat.Close()
	if err = productionWriteFull(chat, []byte{'C', 0}); err != nil {
		t.Fatal(err)
	}
	var latencies []time.Duration
	stop := make(chan struct{})
	chatDone := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				chatDone <- nil
				return
			default:
			}
			begin := time.Now()
			if err := rev4Exchange(chat, len(latencies), 32, 3*time.Second); err != nil {
				chatDone <- err
				return
			}
			latencies = append(latencies, time.Since(begin))
			select {
			case <-stop:
				chatDone <- nil
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	}()
	for _, phase := range []byte{'D', 'U'} {
		begin := time.Now()
		var wg sync.WaitGroup
		fail := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c, err := open()
				if err != nil {
					fail <- err
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(45 * time.Second))
				seed := byte(i + 7)
				if err = productionWriteFull(c, []byte{phase, seed}); err != nil {
					fail <- err
					return
				}
				b := pattern(seed)
				if phase == 'D' {
					got := make([]byte, size)
					_, err = io.ReadFull(c, got)
					if err == nil && !bytes.Equal(b, got) {
						err = fmt.Errorf("download bytes changed")
					}
				} else {
					if err = productionWriteFull(c, b); err == nil {
						ack := make([]byte, 2)
						_, err = io.ReadFull(c, ack)
						if err == nil && string(ack) != "OK" {
							err = fmt.Errorf("upload tail changed")
						}
					}
				}
				if err != nil {
					fail <- fmt.Errorf("phase %c stream %d: %w", phase, i, err)
				}
			}(i)
		}
		wg.Wait()
		close(fail)
		for err := range fail {
			t.Error(err)
		}
		t.Logf("phase=%c application_mbps=%.3f duration_ms=%d", phase, float64(8*size*8)/time.Since(begin).Seconds()/1e6, time.Since(begin).Milliseconds())
		if t.Failed() {
			break
		}
	}
	close(stop)
	if err := <-chatDone; err != nil {
		t.Error("small request failed under phased load", err)
	}
	var sum, max time.Duration
	for _, d := range latencies {
		sum += d
		if d > max {
			max = d
		}
	}
	if len(latencies) > 0 {
		t.Logf("short_requests=%d mean_ms=%d max_ms=%d", len(latencies), (sum / time.Duration(len(latencies))).Milliseconds(), max.Milliseconds())
	}
}
