package mailstream

import (
	"testing"
	"time"
)

func TestRC116SlowRadioInteractiveQueueGuardReducesFlight(t *testing.T) {
	now := time.Now()
	w := deliveryWindow{
		adaptive: true, rate: 450_000, limit: 1 << 20,
		base: 200*time.Millisecond, controlBase: 100*time.Millisecond,
	}
	w.observeInteractive(550*time.Millisecond, now)
	if !w.pacer.guarded || !now.Before(w.controlSlowUntil) {
		t.Fatal("slow-radio interactive spike did not engage congestion protection")
	}
	if w.limit < lowRateMinimumFlight || w.limit >= initialDeliveryWindow {
		t.Fatalf("slow-radio queue limit failed to contract: %d bytes", w.limit)
	}
	if horizon := w.deliveryHorizon(now); horizon > 450*time.Millisecond {
		t.Fatalf("slow-radio queue horizon not constrained: %s", horizon)
	}
	if w.interactiveQueueGuardActive(now.Add(3*time.Second)) {
		t.Fatal("slow-radio queue guard never expires")
	}
}

func TestRC116HighRatePathKeepsQualifiedWindowAndTrigger(t *testing.T) {
	now := time.Now()
	w := deliveryWindow{
		adaptive: true, rate: 32_000_000, limit: 4 << 20,
		base: 200*time.Millisecond, controlBase: 100*time.Millisecond,
	}
	w.observeInteractive(550*time.Millisecond, now)
	if w.pacer.guarded || now.Before(w.controlSlowUntil) {
		t.Fatal("normal 500Mbps path erroneously adopted low-rate queue guard")
	}
	if w.limit != 4<<20 {
		t.Fatalf("normal high-rate flight changed unexpectedly: %d", w.limit)
	}
	w.observeInteractive(850*time.Millisecond, now)
	if !now.Before(w.controlSlowUntil) || w.limit < initialDeliveryWindow {
		t.Fatal("existing high-rate severe-delay protection no longer works")
	}
}
