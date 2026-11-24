package mobile

import (
	"encoding/json"
	"itom.local/core/tunnel"
	"testing"
)

func TestObservationDistinguishesConnectionsFromTunnelStreams(t *testing.T) {
	c := new(Client)
	n := trafficSnapshot{ActiveTCP: 6, Opening: 5, Upload: 100, Download: 200}
	s := tunnel.ClientSnapshot{Stats: tunnel.Stats{TCP: 11}, TransportStreams: 11, Lines: []tunnel.LineDiagnostic{{AccountID: "a01", TCPStreams: 4, UpBps: 8000, RateReady: true}}}
	raw, err := json.Marshal(c.decorateSnapshot(s, n))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["TCP"] != float64(6) || fields["TransportStreams"] != float64(11) || fields["OpeningTCP"] != float64(5) || fields["Upload"] != float64(100) {
		t.Fatalf("confused counter meanings: %s", raw)
	}
	line := fields["Lines"].([]any)[0].(map[string]any)
	if line["UpBps"] != float64(8000) || line["TCPStreams"] != float64(4) || line["RateReady"] != true {
		t.Fatalf("lost line observation: %s", raw)
	}
}
