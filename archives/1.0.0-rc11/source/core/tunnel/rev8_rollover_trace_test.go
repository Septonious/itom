package tunnel

import (
	"testing"
	"time"
)

// Sampling only suppresses redundant trace records, never a spend, fence,
// physical quota charge, or retry. A later report includes every skipped event.
func TestRev8GrantRolloverTraceSampling(t *testing.T) {
	var sampler admissionTraceSampler
	start := time.Unix(1_760_000_000, 0)
	emit, total, delta := sampler.observe(start)
	if !emit || total != 1 || delta != 1 {
		t.Fatalf("initial rollover: emit=%v total=%d delta=%d", emit, total, delta)
	}
	for i := 0; i < 40; i++ {
		emit, _, _ = sampler.observe(start.Add(time.Duration(i+1) * time.Millisecond))
		if emit {
			t.Fatalf("unscheduled trace at event %d", i)
		}
	}
	emit, total, delta = sampler.observe(start.Add(deferredAdmissionTraceInterval))
	if !emit || total != 42 || delta != 41 {
		t.Fatalf("aggregated rollover: emit=%v total=%d delta=%d", emit, total, delta)
	}
	emit, total, delta = sampler.observe(start.Add(2 * deferredAdmissionTraceInterval))
	if !emit || total != 43 || delta != 1 {
		t.Fatalf("subsequent rollover: emit=%v total=%d delta=%d", emit, total, delta)
	}
}
