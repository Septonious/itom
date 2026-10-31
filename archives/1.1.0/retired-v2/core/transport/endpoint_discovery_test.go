package transport

import (
	"bufio"
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryRetainsLateLowerUIDsAndRetriesMissingBodiesWithoutRescanningOldBodies(t *testing.T) {
	x, peer := standbyPeer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	finished := make(chan map[uint32]int, 1)
	go func() {
		counts := map[uint32]int{}
		defer func() { finished <- counts }()
		r := bufio.NewReader(peer)
		round := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			fields := strings.Fields(line)
			tag := fields[0]
			if strings.Contains(line, "(UID)\r\n") {
				round++
				uids := []uint32{100, 300}
				if round >= 2 {
					uids = []uint32{50, 100, 300}
				}
				if round == 4 {
					uids = []uint32{50, 300}
				}
				for n, uid := range uids {
					fmt.Fprintf(peer, "* %d FETCH (UID %d)\r\n", n+1, uid)
				}
			} else {
				uid64, _ := strconv.ParseUint(strings.Split(fields[3], ":")[0], 10, 32)
				uid := uint32(uid64)
				counts[uid]++
				if uid != 300 || round != 1 {
					fmt.Fprintf(peer, "* 1 FETCH (UID %d BODY[] {1}\r\nx)\r\n", uid)
				}
			}
			fmt.Fprintf(peer, "%s OK fetched\r\n", tag)
		}
	}()
	scan := endpointScan{x: x}
	var visited []uint32
	var released [][]byte
	for range 5 {
		if err := scan.poll(ctx, func(b Body) error { visited = append(visited, b.UID); released = append(released, b.Bytes); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(visited, []uint32{100, 50, 300, 100}) {
		t.Fatal("discovery skipped a late or retried body", visited)
	}
	for _, bytes := range released {
		for _, b := range bytes {
			if b != 0 {
				t.Fatal("discovery retained BODY bytes")
			}
		}
	}
	x.Close()
	peer.Close()
	counts := <-finished
	if !reflect.DeepEqual(counts, map[uint32]int{50: 1, 100: 2, 300: 2}) {
		t.Fatal("unchanged old bodies were fetched repeatedly", counts)
	}
}
