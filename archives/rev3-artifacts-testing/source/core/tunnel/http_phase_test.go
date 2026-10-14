package tunnel

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Request handlers capture an immutable run identity. A delayed EOF or byte
// from a previous upload must not affect the next run of the same phase.
type httpPhaseMeasurement struct {
	window *deliveryWindow
	active atomic.Bool
	up     atomic.Int64
	starts sync.Map
}

func (p *httpPhaseMeasurement) firstPayload(identity string, now time.Time) {
	p.starts.LoadOrStore(identity, now)
}

func (p *httpPhaseMeasurement) startupSummary(begin time.Time) (count int, maximum time.Duration) {
	p.starts.Range(func(_, value any) bool { count++; maximum = max(maximum, value.(time.Time).Sub(begin)); return true })
	return
}

type httpPhaseRegistry struct{ runs sync.Map }

func (r *httpPhaseRegistry) start(key string, begin time.Time, duration time.Duration) *httpPhaseMeasurement {
	p := &httpPhaseMeasurement{window: newDeliveryWindow(begin, duration)}
	p.active.Store(true)
	r.runs.Store(key, p)
	return p
}
func (r *httpPhaseRegistry) get(key string) *httpPhaseMeasurement {
	if p, ok := r.runs.Load(key); ok {
		return p.(*httpPhaseMeasurement)
	}
	return nil
}
func TestHTTPRepeatsCannotBorrowOldBytesOrCancellation(t *testing.T) {
	var r httpPhaseRegistry
	now := time.Unix(1000, 0)
	old := r.start("run1-upload", now, 30*time.Second)
	old.window.add(true, 1000, now.Add(time.Second))
	old.active.Store(false)
	fresh := r.start("run2-upload", now.Add(time.Minute), 30*time.Second)
	old.window.add(true, 5000, now.Add(61*time.Second))
	if old.active.Load() || !fresh.active.Load() {
		t.Fatal("old handler cancellation affected new run")
	}
	if old.window.up.Load() != 1000 || fresh.window.up.Load() != 0 {
		t.Fatal("old bytes entered a later measurement")
	}
	if r.get("run1-upload") != old || r.get("run2-upload") != fresh || r.get("unknown") != nil {
		t.Fatal("request identity was conflated")
	}
}
