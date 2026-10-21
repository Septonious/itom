package tunnel

import (
	"math"
	"testing"
)

func stableGoalSpeed(rate, target float64) bool {
	return target > 0 && !math.IsNaN(target) && !math.IsInf(target, 0) && !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate >= .9*target
}

func TestStableGoalSpeedBoundary(t *testing.T) {
	for _, tc := range []struct {
		rate, target float64
		want         bool
	}{
		{125, 125, true}, {112.5, 125, true}, {math.Nextafter(112.5, 0), 125, false},
		{math.NaN(), 125, false}, {math.Inf(1), 125, false}, {125, 0, false},
	} {
		if got := stableGoalSpeed(tc.rate, tc.target); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
