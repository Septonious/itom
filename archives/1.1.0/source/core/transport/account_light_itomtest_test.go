//go:build itomtest

package transport_test

import (
	"itom.local/core/qualification"
	"testing"
	"time"
)

// This gate retains every sample and applies the user's temporary 300ms
// maximum, with 250ms still reported as the preferred target. Functional source
// passes cannot qualify these measurements.
func TestAccountWarmLightQualification(t *testing.T) {
	t.Run("loopback", func(t *testing.T) {
		samples := testAccountTLSHTTPWithWarmSamples(t, nil, nil, 64)
		for _, failure := range qualification.CheckExchanges(samples) {
			t.Error(failure)
		}
	})
	t.Run("fluctuating_command_service", func(t *testing.T) {
		samples := testAccountTLSHTTPWithWarmSamples(t, []time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}, []time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 30 * time.Millisecond}, 64)
		for _, failure := range qualification.CheckExchanges(samples) {
			t.Error(failure)
		}
	})
}
