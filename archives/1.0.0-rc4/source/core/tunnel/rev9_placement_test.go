package tunnel

import (
	"fmt"
	"itom.local/core/mailbox"
	"itom.local/core/wire"
	"testing"
)

type rev9ScoredMail struct {
	mailbox.Transport
	calls int
	cost  int64
}

func (m *rev9ScoredMail) DataQueueCost() int64 { m.calls++; return m.cost }
func TestRev9BulkPlacementSamplesEachCarrierOnce(t *testing.T) {
	g := newStripeGroup(wire.ID{9}, 1)
	var all []*rev9ScoredMail
	var keys []string
	for i := 1; i <= 8; i++ {
		id := fmt.Sprintf("a%02d", i)
		l := stripeTestLane(g, id, false)
		defer l.cancel()
		m := &rev9ScoredMail{cost: int64(9-i) * 100000}
		l.mail = m
		g.lanes[id] = l
		all = append(all, m)
		keys = append(keys, id)
	}
	g.prioritizeBulkTargetsLocked(keys)
	if keys[0] != "a08" || keys[7] != "a01" {
		t.Fatal("placement costs ignored", keys)
	}
	for _, m := range all {
		if m.calls != 1 {
			t.Fatal("queue sampled repeatedly inside sort", m.calls)
		}
	}
}
