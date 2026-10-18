package tunnel

import "testing"

// Without this invariant, a supposedly upward-ramping test can secretly start
// at 150Mbps, queue 4MiB of DATA, and immediately DOWNshift to 10Mbps.
// That stresses a different property and must not be claimed as a speedup test.
func TestProviderSpeedupStartsLowAndDowngradeRemainsSeparate(t *testing.T) {
	steps := []providerCapStep{{Mbps: 10}, {Mbps: 20}, {Mbps: 50}, {Mbps: 150}}
	if got := initialProviderProfileMbps(steps, 150, true); got != 10 {
		t.Fatalf("early profile incorrectly preloaded high-bandwidth queue: %g", got)
	}
	if got := initialProviderProfileMbps(steps, 150, false); got != 150 {
		t.Fatalf("downgrade fixture unexpectedly started low: %g", got)
	}
	if got := initialProviderProfileMbps(nil, 50, true); got != 50 {
		t.Fatalf("empty profile must preserve fallback: %g", got)
	}
}
