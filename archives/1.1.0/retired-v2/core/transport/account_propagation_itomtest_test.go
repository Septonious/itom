//go:build itomtest

package transport_test

import (
	"testing"
	"time"

	"itom.local/core/qualification"
)

// Propagation and bandwidth apply to opaque TCP/TLS bytes on EVERY selected
// account socket, including rendezvous/Noise/roster. Provider processing has no
// synthetic sleep. Endpoints remain on the shared source host, not separate VMs.
func TestAccountWarmLightPropagationQualification(t *testing.T) {
	client := newWireRoute([]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	server := newWireRoute([]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 30 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	samples := testAccountTLSHTTPOnNetwork(t, nil, nil, 64, client, server)
	for _, failure := range qualification.CheckExchanges(samples) {
		t.Error(failure)
	}
}

// The upper end of each requested latency range must also work when it lasts
// throughout an exchange. A fluctuating profile can otherwise hide this corner
// by repeatedly moving one leg into its faster phase during a request.
func TestAccountUpperLatencyLightPropagationQualification(t *testing.T) {
	client := newWireRoute([]time.Duration{50 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	server := newWireRoute([]time.Duration{40 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	samples := testAccountTLSHTTPOnNetwork(t, nil, nil, 16, client, server)
	for _, failure := range qualification.CheckExchanges(samples) {
		t.Error(failure)
	}
}

// The same complete accounting/runtime path must keep an existing TLS stream
// responsive after real application silence. Slightly irregular gaps avoid
// synchronizing requests with the deterministic link-profile period. A longer
// total scenario lifetime accommodates 30s silence; request deadlines do not grow.
func TestAccountQuietLightPropagationQualification(t *testing.T) {
	client := newWireRoute([]time.Duration{10 * time.Millisecond, 50 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	server := newWireRoute([]time.Duration{20 * time.Millisecond, 40 * time.Millisecond, 30 * time.Millisecond}, 250*time.Millisecond, 62_500_000)
	samples := testAccountTLSHTTPScenario(t, nil, nil, 3, client, server, accountHTTPScenario{
		lifetime:  60 * time.Second,
		quietGaps: []time.Duration{1130 * time.Millisecond, 5070 * time.Millisecond, 30190 * time.Millisecond},
	})
	for _, failure := range qualification.CheckExchanges(samples) {
		t.Error(failure)
	}
}
