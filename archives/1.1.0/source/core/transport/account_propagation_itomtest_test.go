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
