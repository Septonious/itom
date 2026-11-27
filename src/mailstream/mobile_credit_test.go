package mailstream

import (
	"testing"
	"time"
)

func TestMobileStreamCreditBackpressure(t *testing.T) {
	for _, tc := range []struct {
		window      int
		outstanding uint64
		mobile      bool
		want        uint64
	}{{streamWindow, 0, true, 1 << 20}, {streamWindow, 2 << 20, true, 0}, {streamWindow, 30 << 20, false, 2 << 20}, {768 << 10, 512 << 10, true, 256 << 10}, {initialStreamWindow, 128 << 10, true, 128 << 10}} {
		if got := streamSendRoom(tc.window, tc.outstanding, tc.mobile); got != tc.want {
			t.Fatalf("%+v: %d", tc, got)
		}
	}
}
func TestMobileFragmentSizeFollowsMeasuredRate(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		want int
	}{{0, 16 << 10}, {100000, 16 << 10}, {500000, 37500}, {1000000, maxPayload - 13}} {
		if got := mobilePayloadLimit(tc.rate); got != tc.want {
			t.Fatalf("%+v: %d", tc, got)
		}
	}
}
func TestReverseReservationBounded(t *testing.T) {
	now := time.Unix(1000, 0)
	var m mobileFlow
	m.reset(true)
	m.probeRate = 100000
	m.reverseDemand = true
	m.observeDelay(350*time.Millisecond, 100*time.Millisecond, now, true)
	if m.probeRate != 100000 || m.pacing(now) != 88000 || m.reverseHoldUntil != now.Add(2*time.Second) {
		t.Fatal(m.probeRate, m.reverseHoldUntil)
	}
	m.observeDelay(350*time.Millisecond, 100*time.Millisecond, now.Add(time.Second), true)
	if m.probeRate != 100000 || m.pacing(now.Add(time.Second)) != 88000 {
		t.Fatal(m.probeRate)
	}
}
