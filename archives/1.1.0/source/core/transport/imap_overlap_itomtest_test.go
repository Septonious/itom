//go:build itomtest

package transport_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"itom.local/core/transport"
)

// Parser service only, not tunnel RTT or useful throughput. Both variants
// execute the same real selected TLS socket and constant 50ms opaque TCP route.
// The peer replies immediately to each command, without scripted command sleeps.
func TestMeasuredDisjointFetchServiceOnOneTLSCarrier(t *testing.T) {
	x := selectedAccountPeerWithNetwork(t, "account1", "fast", transport.DefaultIMAPLimits(), "", false,
		newWireRoute([]time.Duration{50 * time.Millisecond}, time.Second, 62_500_000), func(c net.Conn) {
			r := bufio.NewReader(c)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					return
				}
				switch {
				case strings.HasSuffix(line, " UID FETCH 2:2 (UID BODY.PEEK[])\r\n"):
					fmt.Fprintf(c, "* 2 FETCH (UID 2 BODY[] {4}\r\ndata)\r\n%s OK body\r\n", tag(line))
				case strings.HasSuffix(line, " UID FETCH 1:1 (UID FLAGS)\r\n"):
					fmt.Fprintf(c, "* 1 FETCH (UID 1 FLAGS ())\r\n%s OK flags\r\n", tag(line))
				default:
					t.Error("unexpected parser-service command", line)
					io.Copy(io.Discard, r)
					return
				}
			}
		})
	for sample := range 20 {
		started := time.Now()
		body, err := transport.QueuedBodyForTest(context.Background(), x, 2)
		if err != nil {
			t.Fatal(err)
		}
		flags, err := transport.QueuedFlagsForTest(context.Background(), x, 1)
		if err != nil {
			t.Fatal(err)
		}
		got, err := wait(t, body)
		if err != nil || len(got) != 1 || got[0].UID != 2 || string(got[0].Bytes) != "data" {
			t.Fatal(got, err)
		}
		got, err = wait(t, flags)
		if err != nil || len(got) != 1 || got[0].UID != 1 {
			t.Fatal(got, err)
		}
		elapsed := time.Since(started)
		t.Logf("parser_pair_sample=%d elapsed_ns=%d concurrent_pairs=%d", sample, elapsed.Nanoseconds(), x.Snapshot().ConcurrentFetchPairs)
	}
}
