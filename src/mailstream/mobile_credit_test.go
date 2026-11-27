package mailstream

import "testing"

func TestMobileStreamCreditBackpressure(t *testing.T) {
	for _, tc := range []struct {
		window      int
		outstanding uint64
		mobile      bool
		want        uint64
	}{
		{streamWindow, 0, true, 1 << 20}, {streamWindow, 2 << 20, true, 0}, {streamWindow, 768 << 10, true, 256 << 10}, {initialStreamWindow, 128 << 10, true, 128 << 10}, {streamWindow, 2 << 20, false, 30 << 20},
	} {
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
