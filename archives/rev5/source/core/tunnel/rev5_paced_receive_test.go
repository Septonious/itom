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

	"itom.local/core/mailbox"
)

func paceProviderSocket(t *testing.T, n *providerTestNetwork, c *mailbox.Client, mbps float64) {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, socket := range n.active {
		if socket.originalPeer == c.LocalAddr().String() && socket.originalLocal == c.RemoteAddr().String() && !socket.closed.Load() {
			socket.perSocketDownlink.setMbps(mbps)
			socket.pacedDownstream.Store(true)
			return
		}
	}
	t.Fatal("paced receive socket tuple not found")
}

func TestRev5PerSocketPacingCalibration(t *testing.T) {
	const size = 256 << 10
	results := make(chan error, 4)
	start := time.Now()
	for i := 0; i < 2; i++ {
		network := newProviderTestNetwork(0)
		peer, wire := net.Pipe()
		server := network.wrap("calibration", wire).(*providerTestConn)
		server.perSocketDownlink.setMbps(.3)
		server.pacedDownstream.Store(true)
		deadline := time.Now().Add(12 * time.Second)
		peer.SetDeadline(deadline)
		server.SetDeadline(deadline)
		t.Cleanup(func() { peer.Close(); server.Close() })
		want := bytes.Repeat([]byte{byte(i + 1)}, size)
		go func() { results <- productionWriteFull(server, want) }()
		go func() {
			got := make([]byte, size)
			_, err := io.ReadFull(peer, got)
			if err == nil && !bytes.Equal(got, want) {
				err = fmt.Errorf("pacing calibration corrupted bytes")
			}
			results <- err
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	ideal := time.Duration(int64(size) * 8 * int64(time.Second) / 300000)
	if elapsed < ideal*90/100 || elapsed > ideal*110/100 {
		t.Fatal("uncalibrated per-TCP pacing", elapsed, ideal)
	}
	t.Logf("per_tcp_mbps=0.3 connections=2 separate_downstream_clocks=true bytes_per_tcp=%d elapsed=%s ten_percent_margin=true", size, elapsed)
}

// Four legal 350 KiB MIME bodies take over thirty seconds at the field's
// per-TCP rate. A continuously progressing socket must yield bounded pages,
// rather than hit the thirty-second absolute cap on a four-BODY FETCH.
func TestRev5PacedBodiesYieldWithoutRestartingHealthyReceiver(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	cm, sm := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a01")
	m := cm.(*laneMail)
	defer m.Close()
	defer sm.(*laneMail).Close()
	folder := "down-data-00"
	want, _ := mailbox.Encode(bytes.Repeat([]byte{0x63}, 256<<10))
	for i := 0; i < 4; i++ {
		if err := m.Client.Ensure(ctx, folder); err != nil {
			t.Fatal(err)
		}
		if err := m.Client.Append(ctx, folder, want); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.stageTopology(ctx); err != nil {
		t.Fatal(err)
	}
	reader := m.dataReaderRef()
	paceProviderSocket(t, network, reader, .3)
	probeCtx, stopProbes := context.WithCancel(ctx)
	var probes sync.WaitGroup
	probeErrors := make(chan error, 8)
	probes.Add(1)
	go func() {
		defer probes.Done()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-probeCtx.Done():
				return
			case <-tick.C:
				pingCtx, stop := context.WithTimeout(probeCtx, 1100*time.Millisecond)
				_, err := m.Client.Ping(pingCtx)
				stop()
				if err != nil && probeCtx.Err() == nil {
					select {
					case probeErrors <- err:
					default:
					}
					return
				}
			}
		}
	}()
	defer func() { stopProbes(); probes.Wait() }()
	cur := mailbox.Cursor{}
	for i := 0; i < 4; i++ {
		callbacks := 0
		start := time.Now()
		next, err := m.pageData(ctx, folder, cur, func(row mailbox.Message) error {
			callbacks++
			if !bytes.Equal(row.MIME, want) {
				return fmt.Errorf("physical BODY corrupted")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("paced continuous BODY receive failed: page=%d callbacks=%d elapsed=%s err=%v", i, callbacks, time.Since(start), err)
		}
		if callbacks != 1 || next.After != uint32(i+1) || time.Since(start) > 15*time.Second {
			t.Fatalf("long literal page was not bounded: page=%d callbacks=%d cursor=%+v elapsed=%s", i, callbacks, next, time.Since(start))
		}
		if m.dataReaderRef() != reader || !reader.Ready() {
			t.Fatal("healthy paced socket replaced")
		}
		cur = next
	}
	select {
	case err := <-probeErrors:
		t.Fatal("paced DATA blocked independent control", err)
	default:
	}
	t.Log("per_receive_tcp_mbps=0.3 legal_bodies=4 body_payload_bytes=262144 intact=true same_receive_socket=true control_deadline_ms=1100")
}

// A complete large page legitimately exceeds 250 ms on a healthy shared link.
// Its byte rate, rather than total page latency alone, must preserve batching.
func TestRev5FastLargeBodyPagesKeepAmortizedFetches(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	cm, sm := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a01")
	m := cm.(*laneMail)
	defer m.Close()
	defer sm.(*laneMail).Close()
	folder := "down-data-00"
	if err := m.Client.Ensure(ctx, folder); err != nil {
		t.Fatal(err)
	}
	want, _ := mailbox.Encode(bytes.Repeat([]byte{0x25}, 256<<10))
	for i := 0; i < 9; i++ {
		if err := m.Client.Append(ctx, folder, want); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.stageTopology(ctx); err != nil {
		t.Fatal(err)
	}
	paceProviderSocket(t, network, m.dataReaderRef(), 40)
	cur := mailbox.Cursor{}
	for i, count := range []int{1, 4, 4} {
		seen := 0
		next, err := m.pageData(ctx, folder, cur, func(row mailbox.Message) error {
			seen++
			if !bytes.Equal(row.MIME, want) {
				return fmt.Errorf("healthy BODY changed")
			}
			return nil
		})
		if err != nil || seen != count {
			t.Fatalf("healthy page batching lost: turn=%d expected=%d actual=%d cursor=%+v err=%v", i, count, seen, next, err)
		}
		cur = next
	}
	if cur.After != 9 {
		t.Fatal("healthy receive cursor lost bodies", cur)
	}
}

// A healthy small BODY on a 300 ms path exceeds the old 250 ms classifier.
// Calibrate with a real authenticated NOOP over the same mobile impairment,
// then require batching; propagation cannot become evidence of BODY pacing.
func TestRev5SmallBodiesKeepBatchingOnMobilePropagation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	network := newProviderTestNetwork(0)
	network.mobileEnabled = true
	cm, sm := tlsMailPairWithNetwork(t, ctx, []string{"up", "down"}, network, "a01")
	m := cm.(*laneMail)
	defer m.Close()
	defer sm.(*laneMail).Close()
	folder := "down-data-00"
	if err := m.Client.Ensure(ctx, folder); err != nil {
		t.Fatal(err)
	}
	want, _ := mailbox.Encode(bytes.Repeat([]byte{0x47}, 32<<10))
	for i := 0; i < 9; i++ {
		if err := m.Client.Append(ctx, folder, want); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.stageTopology(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*mailbox.Client{m.Client, m.dataReaderRef()} {
		if !network.markMobileClient(c.LocalAddr().String(), c.RemoteAddr().String()) {
			t.Fatal("mobile socket not scoped")
		}
	}
	if !network.mobile.set(mobileLinkProfile{RTT: 300 * time.Millisecond, Mbps: 40, Seed: 5}) {
		t.Fatal("mobile profile")
	}
	if rtt, err := m.Ping(ctx); err != nil || rtt < 250*time.Millisecond {
		t.Fatal("unloaded path calibration", rtt, err)
	}
	cur := mailbox.Cursor{}
	for i, count := range []int{1, 4, 4} {
		seen := 0
		next, err := m.pageData(ctx, folder, cur, func(row mailbox.Message) error {
			seen++
			if !bytes.Equal(row.MIME, want) {
				return fmt.Errorf("body changed")
			}
			return nil
		})
		if err != nil || seen != count {
			t.Fatalf("mobile propagation lost batching: page=%d want=%d got=%d err=%v", i, count, seen, err)
		}
		cur = next
	}
	if cur.After != 9 {
		t.Fatal("body cursor", cur)
	}
}
