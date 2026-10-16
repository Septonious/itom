package tunnel

import (
	"testing"
	"time"
)

// IMAP advertises IDLE but sends no EXISTS during the wait. After repeated
// audits actually find messages, active chat must stop inheriting the full
// half-second watchdog on both hops. Empty/quiet mailboxes keep normal pacing.
func TestRev7MissingIDLENotificationsBoundActiveChatLatency(t *testing.T) {
	t.Setenv("ITOM_LOAD_SUPPRESS_IDLE_NOTIFICATIONS", "1")
	f := newProductionSupervisorFixtureAccounts(t, 3)
	chat := rev4Open(t, f)
	for i := 0; i < 8; i++ {
		if err := pause(f.ctx, 2*time.Second); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		if err := rev4Exchange(chat, 780+i, 4096, 1100*time.Millisecond); err != nil {
			t.Fatal("chat without IDLE notifications", i, err)
		}
		elapsed := time.Since(started)
		if i >= 4 && elapsed > 550*time.Millisecond {
			t.Fatal("trained fallback retained half-second-per-hop jitter", i, elapsed)
		}
		t.Logf("missing_idle_notification sample=%d rtt_ms=%d trained=%t", i, elapsed.Milliseconds(), i >= 4)
	}
}

func TestRev7NotificationFallbackNeedsActualRowsAndExpires(t *testing.T) {
	now := time.Now()
	var p notificationAuditPolicy
	for i := 0; i < 20; i++ {
		p.waited(false, false)
		p.fetched(0, now)
	}
	if got := p.delay(1, 500*time.Millisecond, now); got != 500*time.Millisecond {
		t.Fatal("silence manufactured notification evidence", got)
	}
	for i := 0; i < 20; i++ {
		p.waited(true, false)
		p.fetched(1, now)
	}
	if got := p.delay(1, 500*time.Millisecond, now); got != 500*time.Millisecond {
		t.Fatal("working IDLE raised command pressure", got)
	}
	for i := 0; i < 2; i++ {
		p.waited(false, false)
		p.fetched(1, now)
	}
	if got := p.delay(1, 500*time.Millisecond, now); got != 50*time.Millisecond {
		t.Fatal("repeated actual notification misses ignored", got)
	}
	if got := p.delay(0, 2*time.Second, now); got != 2*time.Second {
		t.Fatal("quiet cadence changed", got)
	}
	if got := p.delay(1, 500*time.Millisecond, now.Add(31*time.Second)); got != 500*time.Millisecond {
		t.Fatal("fallback never expired", got)
	}
}
