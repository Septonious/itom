package tunnel

import (
	"testing"
	"time"
)

func TestRC8InteractiveFallbackLeavesRoundTripBudget(t *testing.T) {
	cases := []struct{
		best, alternative int64
		want time.Duration
	}{
		{165,585,140*time.Millisecond},
		{300,900,112500*time.Microsecond},
		{500,900,62500*time.Microsecond},
		{700,1200,25*time.Millisecond},
		{300,310,25*time.Millisecond},
		{300,300,100*time.Millisecond},
		{0,500,100*time.Millisecond},
	}
	for _, tc := range cases {
		got := boundedInteractiveGrace(tc.best, tc.alternative)
		if got != tc.want {
			t.Errorf("best=%d alternative=%d grace=%v want=%v",tc.best,tc.alternative,got,tc.want)
		}
		if got>150*time.Millisecond || got<25*time.Millisecond {
			t.Fatalf("bad or unbounded grace: %v",got)
		}
	}
}
