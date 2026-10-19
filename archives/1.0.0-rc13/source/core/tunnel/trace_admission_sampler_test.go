package tunnel

import (
	"sync"
	"testing"
	"time"
)

func TestAdmissionTraceSamplerBoundsAndAccounts(t *testing.T) {
	var s admissionTraceSampler
	at := time.Unix(1720000000, 0)
	emitted, total, delta := s.observe(at)
	if !emitted || total != 1 || delta != 1 {
		t.Fatalf("first %v %d %d", emitted, total, delta)
	}
	for i := 0; i < 1000; i++ {
		if yes, _, _ := s.observe(at.Add(time.Millisecond)); yes {
			t.Fatal("unsampled retry emitted")
		}
	}
	emitted, total, delta = s.observe(at.Add(deferredAdmissionTraceInterval))
	if !emitted || total != 1002 || delta != 1001 {
		t.Fatalf("next %v %d %d", emitted, total, delta)
	}
	if s.total.Load() != 1002 {
		t.Fatal("lost deferred event accounting")
	}
}

func TestAdmissionTraceSamplerConcurrentSingleEmission(t *testing.T) {
	var s admissionTraceSampler
	at := time.Unix(1720000000, 0)
	var wg sync.WaitGroup
	emitted := make(chan struct{}, 500)
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if yes, _, _ := s.observe(at); yes {
				emitted <- struct{}{}
			}
		}()
	}
	wg.Wait()
	if len(emitted) != 1 || s.total.Load() != 500 {
		t.Fatalf("emits=%d total=%d", len(emitted), s.total.Load())
	}
	yes, total, delta := s.observe(at.Add(2 * time.Second))
	if !yes || total != 501 || delta < 1 {
		t.Fatalf("next %v %d %d", yes, total, delta)
	}
}

func TestAdmissionTraceSamplerClockBackwards(t *testing.T) {
	var s admissionTraceSampler
	at := time.Unix(1720000000, 0)
	s.observe(at)
	if yes, _, _ := s.observe(at.Add(-time.Second)); yes {
		t.Fatal("backward clock emitted")
	}
}
