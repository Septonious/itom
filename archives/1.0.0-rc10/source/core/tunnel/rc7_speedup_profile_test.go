package tunnel

import "testing"

func TestRC7SpeedupReallyStartsBeforeApplicationOpen(t *testing.T) {
	steps := []providerCapStep{{Mbps: 10}, {Mbps: 20}, {Mbps: 50}, {Mbps: 150}}
	if got := initialProviderProfileMbps(steps, 150, true); got != 10 {
		t.Fatalf("false startup speedup, got %g", got)
	}
	if got := initialProviderProfileMbps(steps, 150, false); got != 150 {
		t.Fatalf("downgrade control changed, got %g", got)
	}
	if got := initialProviderProfileMbps(nil, 150, true); got != 150 {
		t.Fatalf("empty profile fallback changed, got %g", got)
	}
}
