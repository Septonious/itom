package tunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// Used for the entire supervisor fixture lifetime, including setup and warmup.
// Expected failures are keyed by pointer, never by account or a grace period.
type carrierExitMonitor struct {
	ctx        context.Context
	mu         sync.Mutex
	expected   map[*lane]string
	failures   chan error
	unexpected atomic.Int64
	details    []error
}

func newCarrierExitMonitor(ctx context.Context) *carrierExitMonitor {
	return &carrierExitMonitor{ctx: ctx, expected: make(map[*lane]string), failures: make(chan error, 1)}
}
func (m *carrierExitMonitor) expect(lanes ...*lane) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, l := range lanes {
		if l != nil {
			m.expected[l] = ""
		}
	}
}

// A planned rotation exempts only its exact authenticated protocol transition.
// A timeout or socket failure in that lifetime remains a release failure.
func (m *carrierExitMonitor) expectCode(l *lane, code string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expected[l] = code
}
func (m *carrierExitMonitor) observe(l *lane, err error) {
	if m.ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	expectedCode, expected := m.expected[l]
	delete(m.expected, l)
	m.mu.Unlock()
	if expected && (expectedCode == "" || errorCode(err) == expectedCode) {
		return
	}
	m.unexpected.Add(1)
	if err == nil {
		err = errors.New("unexpected successful carrier stop")
	}
	detail := fmt.Errorf("unexpected account=%s carrier_epoch=%d exit: %w", l.account.ID, l.carrierEpoch, err)
	m.mu.Lock()
	m.details = append(m.details, detail)
	m.mu.Unlock()
	select {
	case m.failures <- detail:
	default:
	}
}
func checkCarrierExitMonitorDoesNotExemptReplacement(t *testing.T) {
	m := newCarrierExitMonitor(context.Background())
	old, replacement := &lane{}, &lane{}
	m.expect(old)
	m.observe(old, errors.New("injected outage"))
	if m.unexpected.Load() != 0 {
		t.Fatal("injected carrier counted as spontaneous failure")
	}
	m.observe(replacement, errors.New("protocol failure"))
	if m.unexpected.Load() != 1 {
		t.Fatal("replacement outage was hidden")
	}
	select {
	case <-m.failures:
	default:
		t.Fatal("measurement did not receive carrier failure")
	}
}
func checkCarrierExitMonitorCountsEveryUnexpectedExit(t *testing.T) {
	m := newCarrierExitMonitor(context.Background())
	for i := 0; i < 3; i++ {
		m.observe(&lane{}, nil)
	}
	if m.unexpected.Load() != 3 {
		t.Fatal("bounded failure channel lost failure count")
	}
}
func checkCarrierExitMonitorIgnoresFixtureTeardown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := newCarrierExitMonitor(ctx)
	cancel()
	m.observe(&lane{}, context.Canceled)
	if m.unexpected.Load() != 0 {
		t.Fatal("fixture teardown counted as protocol failure")
	}
}

func (m *carrierExitMonitor) failureDetails() []error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]error(nil), m.details...)
}

// Resource-independent checks form one scenario; lifecycle and concurrency
// checks in this file remain independently scheduled.
func TestScenarioCarrierObserver(t *testing.T) {
	t.Log("check=checkCarrierExitMonitorDoesNotExemptReplacement")
	checkCarrierExitMonitorDoesNotExemptReplacement(t)
	t.Log("check=checkCarrierExitMonitorCountsEveryUnexpectedExit")
	checkCarrierExitMonitorCountsEveryUnexpectedExit(t)
	t.Log("check=checkCarrierExitMonitorIgnoresFixtureTeardown")
	checkCarrierExitMonitorIgnoresFixtureTeardown(t)
}
