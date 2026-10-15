package tunnel

import (
	"testing"
	"time"
)

func TestHotShortReaderAmortizesCompleteBacklog(t *testing.T) {
	p := receivePagePolicy{}
	if p.size(true) != 4 {
		t.Fatal("unknown bodies unbounded")
	}
	p.observeWithRTT(4, 4, 320*time.Millisecond, 300*time.Millisecond)
	p.observeBodyShape(4, 1024)
	if p.size(true) != 8 {
		t.Fatal("short backlog not amortized")
	}
	p.observeWithRTT(8, 8, 620*time.Millisecond, 600*time.Millisecond)
	p.observeBodyShape(8, 1024)
	if p.size(true) != 16 {
		t.Fatal("verified small backlog capped")
	}
	p.observeBodyShape(1, 128<<10)
	if p.size(true) != 4 {
		t.Fatal("large hot bodies monopolize reader")
	}
	p.observeWithRTT(16, 16, 1000*time.Millisecond, 100*time.Millisecond)
	if p.size(false) != 4 {
		t.Fatal("service delay ignored")
	}
}

func TestParallelFastReaderDemandEndsAtAuthenticatedDrain(t *testing.T) {
	g := newStripeGroup([16]byte{1}, 1)
	g.delivery.ranges = map[deliveryKey]*deliveryRange{}
	for i := uint64(1); i <= 8; i++ {
		g.delivery.ranges[deliveryKey{i, 0}] = &deliveryRange{fast: true, carrier: "a01", admitted: time.Now()}
	}
	if !g.parallelFastTraffic() {
		t.Fatal("parallel return paths can enter idle")
	}
	delete(g.delivery.ranges, deliveryKey{8, 0})
	if g.parallelFastTraffic() {
		t.Fatal("drained burst retained active polling")
	}
	g.delivery.ranges[deliveryKey{8, 0}] = &deliveryRange{fast: true, carrier: "a01", admitted: time.Now().Add(-3 * time.Second)}
	if g.parallelFastTraffic() {
		t.Fatal("stalled debt enabled indefinite hot polling")
	}
	for k := range g.delivery.ranges {
		delete(g.delivery.ranges, k)
	}
	g.delivery.ranges[deliveryKey{1, 0}] = &deliveryRange{fast: true, carrier: "a01", admitted: time.Now()}
	if g.parallelFastTraffic() {
		t.Fatal("single RPC enabled pool-wide polling")
	}
}
