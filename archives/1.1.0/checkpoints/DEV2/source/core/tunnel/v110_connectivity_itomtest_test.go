//go:build itomtest

package tunnel

import (
	"math"
	"testing"
	"time"
)

// User's raw worst-case 4G trace, no synthetic server outage. The target here
// is useful text connectivity, not the high-capacity 100Mbps qualification.
func TestV110WorstRaw4GConnectivity(t *testing.T) {
	t.Setenv("ITOM_HEALTHY_SERVER", "1")
	t.Setenv("ITOM_LOAD_COMMAND_DELAY_MS", "0")
	t.Setenv("ITOM_LOAD_SERVER_RTT_MS", "2")
	t.Setenv("ITOM_LOAD_SERVER_MBPS", "1000")
	t.Setenv("ITOM_LOAD_CLIENT_RTT_MS", "84")
	t.Setenv("ITOM_LOAD_CLIENT_MBPS", "1")
	t.Setenv("ITOM_LOAD_CLIENT_UPLINK_MBPS", "2.5")
	t.Setenv("ITOM_LOAD_LITERAL_PLUS", "1")
	t.Setenv("ITOM_LOAD_BINARY_FETCH", "1")
	f := newProductionSupervisorFixtureAccounts(t, 10)
	c := rev4Open(t, f)
	defer c.Close()
	// Ten direct observations are replay inputs, not a fabricated loss model.
	trace := []time.Duration{84, 63, 43, 67, 23, 60, 55, 56, 21, 66}
	started := time.Now()
	bytes, completed := 0, 0
	for time.Since(started) < 30*time.Second {
		next := *f.network.mobile.profile.Load()
		next.RTT = trace[completed%len(trace)] * time.Millisecond
		// Changing propagation preserves the shared serialization clock and all
		// already queued bytes; a profile transition cannot discard wire debt.
		f.network.mobile.profile.Store(&next)
		if err := rev4Exchange(c, completed, 16<<10, 5*time.Second); err != nil {
			t.Fatal("worst raw4G", completed, err)
		}
		completed++
		bytes += 16 << 10
	}
	seconds := time.Since(started).Seconds()
	mbps := float64(bytes*8) / seconds / 1e6
	if mbps < .1 {
		t.Fatalf("useful request and response rate %.6fMbps below0.1Mbps", mbps)
	}
	if f.targetDials.Load() != 1 || f.carrierExits.unexpected.Load() != 0 {
		t.Fatal("connectivity replaced its application or carrier")
	}
	t.Logf("worst_raw4g useful_mbps_each_direction=%.6f seconds=%.3f completed=%d failed=0 client_down_mbps=1 client_up_mbps=2.5 server_rtt_ms=2 direct_ping_trace_ms=84,63,43,67,23,60,55,56,21,66 field_qualified=false", mbps, seconds, completed)
}

func TestV110AsymmetricFixtureRetainsWireClock(t *testing.T) {
	var m mobileLinkState
	if !m.set(mobileLinkProfile{RTT: 84 * time.Millisecond, Mbps: 1, UplinkMbps: 2.5}) {
		t.Fatal("valid asymmetric path")
	}
	if m.uplink.mbps != 2.5 || m.downlink.mbps != 1 {
		t.Fatal("upload and download caps conflated")
	}
	at := time.Now().Add(time.Second)
	m.uplink.next = at
	next := *m.profile.Load()
	next.RTT = 20 * time.Millisecond
	m.profile.Store(&next)
	if m.uplink.next != at {
		t.Fatal("latency update reset wire debt")
	}
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		if m.set(mobileLinkProfile{Mbps: 1, UplinkMbps: bad}) {
			t.Fatal("invalid asymmetric capacity accepted", bad)
		}
	}
	// Cancellation still joins all normal production workers via the fixture;
	// the link test does not introduce a bypass for transport deadlines.
}
