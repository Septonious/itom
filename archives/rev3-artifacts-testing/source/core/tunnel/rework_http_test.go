package tunnel

import (
	"math"
	"os"
	"testing"
	"time"
)

// This uses the production client/server supervisors, TCP targets, HTTPS and
// real IMAP/TLS sockets. It remains synthetic-provider qualification; the
// Android TUN and real-provider gate must be performed independently.
func TestTransport50ApplicationAcceptance(t *testing.T) {
	if os.Getenv("ITOM_REWORK_HTTP") != "1" {
		t.Skip("set ITOM_REWORK_HTTP=1 for sequential repeated HTTPS qualification")
	}
	t.Setenv("ITOM_FIELD_LOAD", "1")
	t.Setenv("ITOM_FIELD_130_LOAD", "1")
	t.Setenv("ITOM_REWORK_STRICT", "1")
	if os.Getenv("ITOM_FIELD_STREAMS") == "" {
		t.Setenv("ITOM_FIELD_STREAMS", "24")
	}
	if os.Getenv("ITOM_FIELD_SECONDS") == "" {
		t.Setenv("ITOM_FIELD_SECONDS", "30")
	}
	if os.Getenv("ITOM_FIELD_REPEATS") == "" {
		t.Setenv("ITOM_FIELD_REPEATS", "3")
	}
	fieldApplicationMeasurement(t, true)
}
func reworkHTTPAccepted(up, down, tailUp, tailDown float64, upload, download bool, rtt time.Duration, probes, expected, errors int) bool {
	if !upload && !download || rtt <= 0 || rtt >= time.Second || probes != expected || expected < 30 || errors != 0 {
		return false
	}
	valid := func(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) && x >= speedtestAcceptedMbps }
	return (!upload || valid(up) && valid(tailUp)) && (!download || valid(down) && valid(tailDown))
}
func TestReworkHTTPGateRejectsPartialOrOneSidedSuccess(t *testing.T) {
	if !reworkHTTPAccepted(110, 120, 105, 110, true, true, 999*time.Millisecond, 30, 30, 0) {
		t.Fatal("valid full measurement rejected")
	}
	for _, tc := range []struct {
		up, down, tail float64
		rtt            time.Duration
		count, errors  int
	}{
		{110, 49, 110, 500 * time.Millisecond, 30, 0}, {110, 110, 49, 500 * time.Millisecond, 30, 0},
		{110, 110, 110, time.Second, 30, 0}, {110, 110, 110, 500 * time.Millisecond, 29, 0}, {110, 110, 110, 500 * time.Millisecond, 30, 1},
	} {
		if reworkHTTPAccepted(tc.up, tc.down, tc.tail, tc.tail, true, true, tc.rtt, tc.count, 30, tc.errors) {
			t.Fatal("false qualification", tc)
		}
	}
}
