package tunnel

import (
	"testing"
	"time"
)

func TestDeliveryPendingFastPressureOverridesHealthyBulkGrowth(t *testing.T) {
	d := newDeliveryController()
	d.window = 2 * deliveryInitialWindow
	now := time.Now()
	bulk := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now}
	fast := &deliveryRange{key: deliveryKey{3, 0}, end: 1024, fast: true, admitted: now.Add(-2 * time.Second)}
	d.ranges[bulk.key], d.ranges[fast.key] = bulk, fast
	d.bulk, d.fast = bulk.end, fast.end
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 200 * time.Millisecond
	}
	d.maintain(now, true)
	if d.window != deliveryInitialWindow {
		t.Fatal("healthy bulk grew while short DATA was stalled", d.window)
	}
	if d.bulk != bulk.end || d.fast != fast.end || len(d.ranges) != 2 {
		t.Fatal("pressure returned unacknowledged bytes")
	}
	d.progress(3, fast.end, now)
	if d.fast != 0 || d.delivered != fast.end || d.lastFastACKAgeMS != 2000 {
		t.Fatal("interactive ACK accounting changed")
	}
	// The stall must clear, then demand and fresh healthy samples can grow.
	bulk.admitted = now.Add(3 * time.Second)
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 200 * time.Millisecond
	}
	before := d.window
	d.maintain(now.Add(3*time.Second), true)
	if d.window != before {
		t.Fatal("recovery skipped hysteresis", d.window)
	}
}

func TestDeliveryCompletedFastPressureBelongsToItsDrainedEpoch(t *testing.T) {
	d := newDeliveryController()
	d.window = 2 * deliveryInitialWindow
	now := time.Now()
	fast := &deliveryRange{key: deliveryKey{1, 0}, end: 1024, fast: true, admitted: now.Add(-2 * time.Second)}
	d.ranges[fast.key], d.fast = fast, fast.end
	d.progress(1, fast.end, now)
	d.maintain(now, false)
	if d.window != deliveryInitialWindow || d.policy.mode != deliveryIdle {
		t.Fatal("idle epoch did not reset the next bulk burst")
	}
	d.ageCount = 4
	for i := 0; i < d.ageCount; i++ {
		d.ages[i] = 200 * time.Millisecond
	}
	d.maintain(now.Add(time.Second), true)
	if d.window != deliveryInitialWindow+64<<10 || !d.fastPressureUntil.IsZero() {
		t.Fatal("a drained epoch's historical stall throttled fresh useful progress", d.window)
	}
	if d.bulk != 0 || d.fast != 0 || d.delivered != fast.end {
		t.Fatal("short pressure created credit")
	}
}

func TestDeliveryFastPressureHasQueueHeadroomAndKeepsDebt(t *testing.T) {
	d := newDeliveryController()
	now := time.Now()
	bulk := &deliveryRange{key: deliveryKey{1, 0}, end: 32 << 10, admitted: now}
	fast := &deliveryRange{key: deliveryKey{3, 0}, end: 1024, fast: true, admitted: now.Add(-1200 * time.Millisecond)}
	d.ranges[bulk.key], d.ranges[fast.key] = bulk, fast
	d.bulk, d.fast = bulk.end, fast.end
	for epoch := 0; epoch < 12; epoch++ {
		at := now.Add(time.Duration(epoch) * time.Second)
		bulk.admitted = at
		d.ageCount = 4
		for i := 0; i < d.ageCount; i++ {
			d.ages[i] = 200 * time.Millisecond
		}
		d.maintain(at, true)
		if d.window >= deliveryInitialWindow {
			t.Fatal("late short DATA cannot reduce the initial queue budget", d.window)
		}
	}
	if d.window != deliveryMinWindow || d.window < 64<<10 {
		t.Fatal("pressure exceeded bounded recovery budget", d.window)
	}
	if d.bulk != bulk.end || d.fast != fast.end || d.delivered != 0 || len(d.ranges) != 2 {
		t.Fatal("smaller queue returned undelivered bytes")
	}
}
