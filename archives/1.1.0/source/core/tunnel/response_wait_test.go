package tunnel

import (
	"testing"
	"time"
)

func TestResponseSearchDrainsThenUsesBoundedNotificationWait(t *testing.T) {
	var p responseWaitPolicy
	p.request()
	for i := 0; i < 2; i++ {
		if p.empty(true, false, 0) {
			t.Fatal("request skipped its initial visibility search")
		}
	}
	if !p.empty(true, false, 0) {
		t.Fatal("empty response search remained in poll mode")
	}
	if p.empty(true, true, 2) {
		t.Fatal("visible tail was delayed")
	}
	if p.empty(true, false, 0) {
		t.Fatal("visible tail did not reset search")
	}
	p.request()
	if p.empty(false, false, 0) {
		t.Fatal("quiet reader acquired hot polling")
	}
	for _, direct := range []time.Duration{0, time.Millisecond, 5 * time.Millisecond, 40 * time.Millisecond, 100 * time.Millisecond, time.Second} {
		if d := p.poll(direct); d < 2*time.Millisecond || d > 10*time.Millisecond {
			t.Fatal("unbounded poll delay", direct, d)
		}
		if d := p.audit(direct); d < 20*time.Millisecond || d > 150*time.Millisecond {
			t.Fatal("unbounded audit delay", direct, d)
		}
	}
	if p.audit(5*time.Millisecond) >= p.audit(40*time.Millisecond) {
		t.Fatal("audit does not track unloaded route")
	}
}
