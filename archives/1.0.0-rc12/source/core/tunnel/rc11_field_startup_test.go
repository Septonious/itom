package tunnel

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"itom.local/core/wire"
)

// Field-shaped repeated retry bursts, real retained debt, no historical client
// wallet. Production supervisors, TLS, fsync and verified bytes are exercised.
// This regression does not qualify real provider throughput or application RTT.
func TestRC11HomeRetainedRepeatedMultiStartupAndTail(t *testing.T) {
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
	f := newProductionSupervisorFixtureState(t, 4, rev4RetainedState(t))
	logical, generation := f.client.logicalSession, f.client.generation
	chat := rev4Open(t, f)
	defer chat.Close()
	_, port, _ := net.SplitHostPort(f.listener.Addr().String())
	var targetPort int
	fmt.Sscanf(port, "%d", &targetPort)
	for round := 0; round < 3; round++ {
		var wg sync.WaitGroup
		failures := make(chan error, 24)
		start := make(chan struct{})
		for i := 0; i < 24; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				ctx, cancel := context.WithTimeout(f.ctx, 8*time.Second)
				defer cancel()
				stream, _, err := f.client.Open(ctx, wire.Connect{Host: "127.0.0.1", Port: uint16(targetPort)})
				if err != nil {
					failures <- fmt.Errorf("round %d OPEN %d: %w", round, i, err)
					return
				}
				defer stream.Close()
				conn, ok := stream.(net.Conn)
				if !ok {
					failures <- fmt.Errorf("missing stream deadline")
					return
				}
				size := 32 << 10
				if i < 8 {
					size = 256 << 10
				}
				if err := rev4Exchange(conn, round*100+i, size, 8*time.Second); err != nil {
					failures <- fmt.Errorf("round %d stream %d tail: %w", round, i, err)
				}
			}(i)
		}
		close(start)
		for i := 0; i < 4; i++ {
			begin := time.Now()
			if err := rev4Exchange(chat, 1000+round*10+i, 256, 3*time.Second); err != nil {
				t.Error("interactive request under ramp-up", err)
				break
			}
			t.Logf("round=%d interactive_ms=%d", round, time.Since(begin).Milliseconds())
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Error(err)
		}
		if t.Failed() {
			return
		}
		if err := rev4Exchange(chat, 2000+round, 128<<10, 5*time.Second); err != nil {
			t.Fatal("next transfer after MULTI tail", err)
		}
	}
	if f.client.logicalSession != logical || f.client.generation != generation {
		t.Fatal("logical session replaced")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.serverLanes {
		if err := l.data.Audit(); err != nil {
			t.Fatal(err)
		}
	}
}
