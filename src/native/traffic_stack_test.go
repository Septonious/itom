package mobile

import (
	"encoding/json"
	"testing"

	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

func TestNativeTCPDiagnosticsRetainCumulativeCounters(t *testing.T) {
	c := new(Client)
	if s := c.trafficSnapshot(); s.TCPRetransmits != 0 || s.TCPTimeouts != 0 {
		t.Fatal("unattached client reported traffic")
	}
	c.stack = stack.New(stack.Options{})
	defer c.stack.Close()
	stats := c.stack.Stats()
	stats.TCP.Retransmits.IncrementBy(3)
	stats.TCP.Timeouts.IncrementBy(2)
	stats.TCP.SegmentSendErrors.Increment()
	stats.DroppedPackets.IncrementBy(4)
	raw, err := json.Marshal(c.trafficSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	// Read numeric fields independently of booleans and other snapshot values.
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	decoded := make(map[string]uint64)
	for _, key := range []string{"TCPRetransmits", "TCPTimeouts", "TCPSendErrors", "TUNDroppedPackets"} {
		var decodedValue uint64
		if err = json.Unmarshal(fields[key], &decodedValue); err != nil {
			t.Fatal(err)
		}
		decoded[key] = decodedValue
	}
	if decoded["TCPRetransmits"] != 3 || decoded["TCPTimeouts"] != 2 || decoded["TCPSendErrors"] != 1 || decoded["TUNDroppedPackets"] != 4 {
		t.Fatalf("lost native transport evidence: %s", raw)
	}
	c.stack.Close()
	if s := c.trafficSnapshot(); s.TCPRetransmits != 3 || s.TCPTimeouts != 2 {
		t.Fatal("closing the TUN stack erased cumulative evidence")
	}
}
