package tunnel

import (
	"testing"
	"time"
)

// Capture cold-start latency in the per-line snapshot even after ring rotation.
func TestInitialReadyLatencySnapshot(t *testing.T) {
	d := &diagnostic{}
	d.beginConnect()
	d.mu.Lock()
	d.connectingAt = time.Now().Add(-120 * time.Millisecond)
	d.mu.Unlock()
	l := &lane{diag: d}
	l.stage("ready")
	first := d.snapshot().InitialReadyMS
	if first < 100 {
		t.Fatalf("startup duration absent: %dms", first)
	}
	l.stage("ready")
	if d.snapshot().InitialReadyMS != first {
		t.Fatal("repeated stage reset ready duration")
	}
	d.beginConnect()
	if d.snapshot().InitialReadyMS != 0 {
		t.Fatal("next attempt inherited startup duration")
	}
}
