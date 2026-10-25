package transport_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"itom.local/core/transport"
)

func TestExhaustedUIDNumbersKeepInventoryAliveWithoutWrappingOrReplacingCarrier(t *testing.T) {
	var late atomic.Bool
	x := selectedPeer(t, func(c net.Conn) {
		r := bufio.NewReader(c)
		for {
			if _, err := r.Peek(1); err != nil {
				return
			}
			line := readLine(t, r)
			switch {
			case strings.HasSuffix(line, " (UID)\r\n"):
				sequence := 1
				if late.Load() {
					fmt.Fprint(c, "* 1 FETCH (UID 7)\r\n")
					sequence++
				}
				fmt.Fprintf(c, "* %d FETCH (UID 4294967295)\r\n", sequence)
			case strings.Contains(line, "UID FETCH 4294967295:4294967295"):
				fmt.Fprint(c, "* 1 FETCH (UID 4294967295 BODY[] {1}\r\nx)\r\n")
			case strings.Contains(line, "UID FETCH 7:7"):
				fmt.Fprint(c, "* 1 FETCH (UID 7 BODY[] {1}\r\ny)\r\n")
			default:
				t.Error("exhausted selector wrapped or guessed a nonexistent UID", line)
				return
			}
			fmt.Fprintf(c, "%s OK fetched\r\n", tag(line))
		}
	})
	var delivered []uint32
	i := newTestInbox(t, x, func(_ uint32, b transport.Body) (transport.InboxDelivery, error) {
		return transport.InboxAction(func() error { delivered = append(delivered, b.UID); return nil }), nil
	})
	if p, err := i.Poll(context.Background()); err != nil || p.Applied != 1 {
		t.Fatal(p, err)
	}
	if p, err := i.Poll(context.Background()); err != nil || p.Applied != 0 {
		t.Fatal("UID exhaustion stopped healthy discovery", p, err)
	}
	// Keep the maximum UID live: there is still no legal successor. A newly
	// visible lower UID must be found by full inventory without a reset.
	late.Store(true)
	if p, err := i.Poll(context.Background()); err != nil || p.Applied != 1 || x.Snapshot().Closed {
		t.Fatal("late lower UID was lost after allocation exhaustion", p, err, x.Snapshot())
	}
	if !reflect.DeepEqual(delivered, []uint32{^uint32(0), 7}) {
		t.Fatal(delivered)
	}
}
