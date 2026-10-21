package tunnel

import (
	"context"
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

type noPushResponseMailbox struct{ fieldEmptyMailbox }

func (*noPushResponseMailbox) ReaderIdleCapable() bool        { return false }
func (*noPushResponseMailbox) ReaderDirectRTT() time.Duration { return 2 * time.Millisecond }
func (*noPushResponseMailbox) WaitReadWake(context.Context, string, time.Duration, <-chan struct{}) (bool, bool, bool, error) {
	return false, false, false, nil
}

func TestResponseSearchWithoutIdleKeepsLegacyEmptyPollBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &noPushResponseMailbox{}
	l := &lane{ctx: ctx, mail: m, pages: make(chan pageResult, 224), readWake: make(chan struct{}, 1)}
	l.active.Store(1)
	l.armReaderHot(time.Second)
	l.startReader()
	defer func() { cancel(); l.workers.Wait() }()
	time.Sleep(600 * time.Millisecond)
	if polls := m.polls.Load(); polls > 14 {
		t.Fatal("unsupported IDLE caused a hot FETCH storm", polls)
	}
}

func TestObservedResponseSearchKeepsProviderCapabilities(t *testing.T) {
	provider := &noPushResponseMailbox{}
	observed := &observedMail{Transport: provider}
	if observed.ReaderIdleCapable() || observed.ReaderDirectRTT() != 2*time.Millisecond {
		t.Fatal("diagnostics changed adapter timing capabilities")
	}
}
