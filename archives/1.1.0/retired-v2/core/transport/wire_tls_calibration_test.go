package transport_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"itom.local/core/transport"
)

func TestSelectedTLSNoopSamplesTheWireRouteWithoutCommandSleeps(t *testing.T) {
	route := newWireRoute([]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	x := selectedAccountPeerWithNetwork(t, "account1", "fast", transport.DefaultIMAPLimits(), "", false, route, func(c net.Conn) {
		reader := bufio.NewReader(c)
		for {
			if _, err := reader.Peek(1); err != nil {
				return
			}
			line := readLine(t, reader)
			if !strings.Contains(line, " NOOP") {
				t.Error("unexpected calibration command", line)
				return
			}
			fmt.Fprintf(c, "%s OK calibration\r\n", tag(line))
		}
	})
	defer x.Close()
	for probe := range 20 {
		started := time.Now()
		command, err := x.Noop(context.Background())
		if err == nil {
			_, err = command.Wait(context.Background())
		}
		elapsed := time.Since(started)
		t.Logf("encrypted_noop_probe=%d elapsed_ns=%d configured_rtt_ns=[10000000,50000000,20000000,40000000] command_sleep_ns=0", probe, elapsed.Nanoseconds())
		if err != nil || elapsed < 10*time.Millisecond || elapsed > 250*time.Millisecond {
			t.Error("wire calibration outside light-load completion bound", probe, elapsed, err)
		}
	}
}
