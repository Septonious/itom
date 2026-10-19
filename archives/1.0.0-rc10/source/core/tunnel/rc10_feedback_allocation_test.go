package tunnel

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

func rc2FeedbackQuartile(samples []time.Duration, upper bool) time.Duration {
	if len(samples) < 4 {
		return 0
	}
	values := append([]time.Duration(nil), samples...)
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := (len(values) - 1) / 4
	if upper {
		index = (len(values) - 1) * 3 / 4
	}
	return min(2*time.Second, values[index])
}

func TestRC10FeedbackQuartilesPreserveRC2PolicyAndSampleRings(t *testing.T) {
	random := rand.New(rand.NewSource(714))
	for trial := 0; trial < 64; trial++ {
		var d deliveryController
		for i := range d.lowFlightAges {
			d.lowFlightAges[i] = time.Duration(random.Intn(8)) * 500 * time.Millisecond
		}
		for i := range d.fastBaselineAges {
			d.fastBaselineAges[i] = time.Duration(random.Intn(8)) * 500 * time.Millisecond
		}
		low, fast := d.lowFlightAges, d.fastBaselineAges
		for count := 0; count <= len(low); count++ {
			d.lowFlightCount = count
			if got, want := d.acknowledgementFloor(), rc2FeedbackQuartile(low[:count], false); got != want {
				t.Fatalf("lower quartile count=%d got=%s want=%s", count, got, want)
			}
		}
		for count := 0; count <= len(fast); count++ {
			d.fastBaselineCount = count
			if got, want := d.fastPathBaseline(), rc2FeedbackQuartile(fast[:count], true); got != want {
				t.Fatalf("upper quartile count=%d got=%s want=%s", count, got, want)
			}
		}
		if d.lowFlightAges != low || d.fastBaselineAges != fast {
			t.Fatal("feedback read mutated a retained sample ring")
		}
	}
}

func TestRC10FeedbackQuartilesDoNotAllocateWhileHoldingGroupMutex(t *testing.T) {
	var d deliveryController
	d.lowFlightCount, d.fastBaselineCount = len(d.lowFlightAges), len(d.fastBaselineAges)
	if got := testing.AllocsPerRun(100, func() {
		_ = d.acknowledgementFloor()
		_ = d.fastPathBaseline()
	}); got != 0 {
		t.Fatalf("bounded feedback reads allocated: %g", got)
	}
}

func BenchmarkRC10FeedbackQuartiles(b *testing.B) {
	var d deliveryController
	d.lowFlightCount, d.fastBaselineCount = len(d.lowFlightAges), len(d.fastBaselineAges)
	for i := range d.fastBaselineAges {
		d.fastBaselineAges[i] = time.Duration(16-i) * time.Millisecond
	}
	for i := range d.lowFlightAges {
		d.lowFlightAges[i] = time.Duration(8-i) * time.Millisecond
	}
	b.Run("RC2-copy-and-reflect-sort", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = rc2FeedbackQuartile(d.lowFlightAges[:d.lowFlightCount], false)
			_ = rc2FeedbackQuartile(d.fastBaselineAges[:d.fastBaselineCount], true)
		}
	})
	b.Run("bounded-stack-copy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = d.acknowledgementFloor()
			_ = d.fastPathBaseline()
		}
	})
}
