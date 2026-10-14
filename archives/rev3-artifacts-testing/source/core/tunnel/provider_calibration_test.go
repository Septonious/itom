package tunnel

import (
	"bytes"
	"io"
	"math/rand"
	"net"
	"testing"
	"time"
)

// This measures the impairment device independently of ITOM. The same limiter
// is shared by every simulated mobile connection in each physical direction.
// Full duplex and aggregate shared-medium modes are named explicitly.
func calibrateProviderAccess(t *testing.T, profile mobileLinkProfile) {
	t.Helper()
	network := newProviderTestNetwork(0)
	network.mobileEnabled = true
	if !network.mobile.set(profile) {
		t.Fatal("calibration profile")
	}
	const size = 2 << 20
	payload := make([]byte, size)
	_, _ = rand.New(rand.NewSource(714)).Read(payload) // deterministic incompressible input
	results := make(chan error, 8)
	begin := time.Now()
	for connection := 0; connection < 2; connection++ {
		peer, wire := net.Pipe()
		server := network.wrap("calibration", wire).(*providerTestConn)
		server.mobileClient.Store(true)
		t.Cleanup(func() { peer.Close(); server.Close() })
		deadline := time.Now().Add(15 * time.Second)
		peer.SetDeadline(deadline)
		server.SetDeadline(deadline)
		for _, direction := range []struct{ src, dst net.Conn }{{peer, server}, {server, peer}} {
			go func(src net.Conn) {
				for offset := 0; offset < size; offset += 16406 {
					if err := productionWriteFull(src, payload[offset:min(offset+16406, size)]); err != nil {
						results <- err
						return
					}
				}
				results <- nil
			}(direction.src)
			go func(dst net.Conn) {
				got := make([]byte, size)
				_, err := io.ReadFull(dst, got)
				if err == nil && !bytes.Equal(payload, got) {
					err = io.ErrUnexpectedEOF
				}
				results <- err
			}(direction.dst)
		}
	}
	for i := 0; i < 8; i++ {
		if err := <-results; err != nil {
			t.Fatal("access calibration integrity", err)
		}
	}
	elapsed := time.Since(begin)
	wireBytes := 2 * size
	if profile.SharedMedium {
		wireBytes *= 2
	}
	ideal := profile.RTT/2 + time.Duration(float64(wireBytes*8)/profile.Mbps/1e6*float64(time.Second))
	if elapsed < ideal*85/100 || elapsed > ideal*115/100+100*time.Millisecond {
		t.Fatal("uncalibrated shaper", elapsed, ideal)
	}
	t.Logf("access_calibration mode=%s connections=2 incompressible_bytes_per_direction=%d cap_mbps=%.3f path_rtt_ms=%d elapsed_ns=%d ideal_ns=%d verified=true", profile.medium(), 2*size, profile.Mbps, profile.RTT.Milliseconds(), elapsed.Nanoseconds(), ideal.Nanoseconds())
}

func TestProviderAccess20Calibration(t *testing.T) {
	if raceInstrumented { // integrity remains checked in other relay race tests
		t.Log("speed calibration uses the non-instrumented acceptance runner")
		return
	}
	calibrateProviderAccess(t, mobileLinkProfile{RTT: 20 * time.Millisecond, Mbps: 20, Seed: 714})
}
