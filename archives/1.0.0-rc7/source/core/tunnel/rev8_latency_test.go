package tunnel

import (
	"testing"
	"time"
)

func TestRev8LearnedFastQueueAllowanceDoesNotScaleWithRTT(t *testing.T) {
	for _, baseline := range []time.Duration{180, 400, 850, 1800} {
		d := newDeliveryController()
		d.fastBaselineCount = 8
		for i := range d.fastBaselineAges {
			d.fastBaselineAges[i] = baseline * time.Millisecond
		}
		floor := d.fastPathBaseline()
		if got := d.fastAgeBudget(); got != floor+100*time.Millisecond {
			t.Fatalf("baseline=%s permits extra queue %s", floor, got-floor)
		}
		// Delay feedback must never release physical or logical debt.
		if d.bulk != 0 || d.fast != 0 || d.delivered != 0 {
			t.Fatal("latency policy changed delivery accounting")
		}
	}
}

func TestRev8MissingNotificationFallbackBoundsAddedPollingJitter(t *testing.T) {
	now := time.Now()
	var policy notificationAuditPolicy
	for i := 0; i < 2; i++ {
		policy.waited(false, false)
		policy.fetched(1, now)
	}
	if delay := policy.delay(1, 500*time.Millisecond, now); delay != 50*time.Millisecond {
		t.Fatal("missed notifications still add excessive polling delay", delay)
	}
	if delay := policy.delay(0, 2*time.Second, now); delay != 2*time.Second {
		t.Fatal("idle account polling changed", delay)
	}
	if delay := policy.delay(1, 500*time.Millisecond, now.Add(31*time.Second)); delay != 500*time.Millisecond {
		t.Fatal("temporary fallback did not expire", delay)
	}
}

// productionSampleSummary sorts RTTs for percentile consumers. Jitter must
// retain temporal adjacency, otherwise a heavily varying path looks stable.
func productionRTTVariation(samples []productionProbeSample, user int) []time.Duration {
	var changes []time.Duration
	var previous time.Duration
	havePrevious := false
	for _, sample := range samples {
		if sample.User != user || sample.Status != "ok" {
			havePrevious = false
			continue
		}
		if havePrevious {
			delta := sample.RTT - previous
			if delta < 0 {
				delta = -delta
			}
			changes = append(changes, delta)
		}
		previous, havePrevious = sample.RTT, true
	}
	return changes
}

func TestRev8JitterUsesChronologicalSamplesNotSortedPercentiles(t *testing.T) {
	samples := []productionProbeSample{{User: 0, Status: "ok", RTT: 100 * time.Millisecond}, {User: 0, Status: "ok", RTT: 400 * time.Millisecond}, {User: 0, Status: "ok", RTT: 200 * time.Millisecond}}
	productionSampleSummary(samples, 0)
	changes := productionRTTVariation(samples, 0)
	if len(changes) != 2 || changes[0] != 300*time.Millisecond || changes[1] != 200*time.Millisecond {
		t.Fatal("sorted RTTs concealed jitter", changes)
	}
	samples[1].Status = "timeout"
	if len(productionRTTVariation(samples, 0)) != 0 {
		t.Fatal("failed probe bridged as successful adjacent sample")
	}
}
