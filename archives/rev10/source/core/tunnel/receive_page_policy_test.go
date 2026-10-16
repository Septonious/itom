package tunnel

import (
	"testing"
	"time"
)

func TestReceiveBatchAmortizesBacklogWithoutDelayingHotOrEmptyMail(t *testing.T) {
	var p receivePagePolicy
	if p.size(false) != 4 {
		t.Fatal("unbounded cold read")
	}
	for i := 0; i < 10; i++ {
		n := p.size(false)
		p.observe(n, int(n), 65*time.Millisecond)
	}
	if p.size(false) != 16 || p.size(true) != 4 {
		t.Fatal("backlog cannot drain or short response loses small turn", p.limit)
	}
	p.observe(4, 0, 65*time.Millisecond)
	if p.size(false) != 4 {
		t.Fatal("empty polling retained oversized turn")
	}
	p.observe(4, 4, 65*time.Millisecond)
	p.observe(8, 8, 65*time.Millisecond)
	p.observe(16, 16, 500*time.Millisecond)
	if p.size(false) != 4 {
		t.Fatal("slow literal cannot reduce next read turn")
	}
}

func TestMobilePropagationDoesNotMasqueradeAsReceiveBacklog(t *testing.T) {
	var p receivePagePolicy
	for range 3 {
		n := p.size(false)
		p.observeWithRTT(n, int(n), 375*time.Millisecond, 300*time.Millisecond)
	}
	if p.size(false) != 16 || p.size(true) != 4 {
		t.Fatal("RTT pinned receive batching", p.limit)
	}
	p.observeWithRTT(16, 16, 800*time.Millisecond, 300*time.Millisecond)
	if p.size(false) != 4 {
		t.Fatal("real serialization pressure did not shrink receive turn")
	}
	m := &laneMail{}
	m.providerRTT.Store(int64(300 * time.Millisecond))
	if m.receiveProgressBudget() < 2*time.Second || m.receiveProgressBudget() > 3*time.Second {
		t.Fatal("mobile silence bound", m.receiveProgressBudget())
	}
	m.providerRTT.Store(int64(time.Hour))
	if m.receiveProgressBudget() != 10*time.Second {
		t.Fatal("receive silence bound escaped cap")
	}
}
