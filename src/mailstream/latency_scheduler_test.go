package mailstream

import (
	"testing"
	"time"
)

func TestRC116EmptyFetchSamplesWithoutSeparateProbe(t *testing.T) {
	p := &mailPipe{}
	p.observeEmptyControlFetch(time.Now().Add(-20 * time.Millisecond))
	if p.latency.Load() < int64(10*time.Millisecond) || p.latencyAt.Load() == 0 {
		t.Fatalf("empty FETCH not measured: %s", time.Duration(p.latency.Load()))
	}
	previous := p.latency.Load()
	p.observeEmptyControlFetch(time.Now().Add(-30 * time.Millisecond))
	if p.latency.Load() <= previous {
		t.Fatal("empty FETCH EWMA failed to update")
	}
}
