package tunnel

import (
	"itom.local/mailstream"
	"slices"
	"testing"
	"time"
)

// Replays the relevant counters of the 2026-11-28 13:48:10–16 UTC
// server capture. Identities are replaced by the generated fixture accounts.
// A stale download backlog plus a few received bytes in the opposite direction
// must not be interpreted as proof that ten healthy active paths are saturated.
func TestRC23FieldReplayStaleBacklogDoesNotExpandGrant(t *testing.T) {
	a, _ := storageFixture(t)
	a.grants["one"] = slices.Clone(a.accounts[:10])
	rows := []struct {
		rx, pending uint64
		control     int64
		imap        []int64
	}{
		{5706602, 196008, 216, []int64{4, 2, 4, 3, 8, 2, 7, 6, 3, 2}},
		{5706648, 196008, 244, []int64{3, 2, 13, 3, 16, 2, 15, 14, 4, 2}},
		{5721621, 196496, 186, []int64{2, 2, 12, 15, 11, 2, 10, 10, 24, 2}},
		{5721660, 196496, 207, []int64{4, 2, 10, 4, 9, 3, 13, 8, 4, 3}},
	}
	at := time.Unix(100, 0)
	for i, r := range rows {
		m := adaptiveSnapshot(a, "one", 0)
		j := 0
		for k := range m.Lines {
			if m.Lines[k].Allocated {
				m.Lines[k].IMAPLatencyMillis = r.imap[j]
				m.Lines[k].QueuedMIMEBytes = 0
				j++
			}
		}
		s := mailstream.Snapshot{BulkDelivered: 12557829, ReceivedPayload: r.rx, BulkOutstanding: r.pending, InteractiveReceiptRTTMS: r.control, InteractiveReceiptMinRTTMS: 161, BulkSmoothedRTTMS: 358, MobilePacing: true, Retransmissions: 6}
		if a.observeDemand("one", m, s, at.Add(time.Duration(i)*2*time.Second)) && len(a.grant("one")) > 10 {
			t.Fatalf("field replay sample %d: expanded despite zero download progress and no writer pressure: %v", i, a.grant("one"))
		}
	}
	if len(a.grant("one")) > 10 {
		t.Fatal("unjustified expansion")
	}
}

// A direction must demonstrate productive pressure, not just traffic on the
// opposite direction or a stale outstanding counter.
func TestV160DirectionalGrantPressure(t *testing.T) {
	for _, tc := range []struct {
		name           string
		sent, received uint64
		queued, unsent int
		expand         bool
	}{
		{"opposite direction", 0, 200000, 128 << 10, 128 << 10, false},
		{"no writer pressure", 200000, 0, 0, 128 << 10, false},
		{"no unsent data", 200000, 0, 128 << 10, 0, false},
		{"50kbps light traffic", 12500, 0, 128 << 10, 128 << 10, false},
		{"productive outgoing", 200000, 0, 128 << 10, 128 << 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := storageFixture(t)
			at := time.Unix(100, 0)
			for i := 0; i < 4; i++ {
				m := adaptiveSnapshot(a, "one", uint64(i)*200000)
				for j := range m.Lines {
					if m.Lines[j].Allocated {
						m.Lines[j].QueuedMIMEBytes = tc.queued
					}
				}
				s := mailstream.Snapshot{BulkOutstanding: 256 << 10, UnsentBulkBytes: tc.unsent, BulkDelivered: uint64(i) * tc.sent, ReceivedPayload: uint64(i) * tc.received, InteractiveReceiptRTTMS: 180, InteractiveReceiptMinRTTMS: 160}
				a.observeDemand("one", m, s, at.Add(time.Duration(i)*2*time.Second))
			}
			if got := len(a.grant("one")) > 8; got != tc.expand {
				t.Fatal("expansion", got, "expected", tc.expand)
			}
		})
	}
}
