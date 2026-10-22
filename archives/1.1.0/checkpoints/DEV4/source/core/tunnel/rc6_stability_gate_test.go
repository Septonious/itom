package tunnel

import (
	"testing"
	"time"
)

const rc6StabilityRTTLimit = 750 * time.Millisecond

func TestRC6StrictLoadedRTTGate(t *testing.T) {
	for _, tc := range []struct {
		rtt  time.Duration
		pass bool
	}{
		{350 * time.Millisecond, true}, {rc6StabilityRTTLimit - time.Nanosecond, true},
		{rc6StabilityRTTLimit, false}, {903 * time.Millisecond, false},
		{1070 * time.Millisecond, false}, {0, false},
	} {
		if got := tc.rtt > 0 && tc.rtt < rc6StabilityRTTLimit; got != tc.pass {
			t.Fatalf("RTT %v got %v", tc.rtt, got)
		}
	}
}
