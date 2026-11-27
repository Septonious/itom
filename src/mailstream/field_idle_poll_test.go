package mailstream

import (
	"testing"
	"time"
)

func TestDesktopIdleControlPolling(t *testing.T) {
	id := SessionID{1}
	l := &IMAPLink{attached: id, grantSession: id, grant: map[string]bool{"a": true}}
	a := &mailAccountState{link: l, config: MailAccount{ID: "a"}}
	if !a.controlIdleEligible() {
		t.Fatal("ready desktop not eligible")
	}
	for _, tc := range []struct {
		n    int
		want time.Duration
	}{{0, time.Millisecond}, {7, time.Millisecond}, {8, 4 * time.Millisecond}, {31, 4 * time.Millisecond}, {32, 8 * time.Millisecond}, {100, 8 * time.Millisecond}} {
		if got := idleControlDelay(time.Millisecond, tc.n, true); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	if idleControlDelay(40*time.Millisecond, 32, true) != 40*time.Millisecond {
		t.Fatal("standby changed")
	}
	if idleControlDelay(time.Millisecond, 32, false) != time.Millisecond {
		t.Fatal("mobile changed")
	}
	l.lastBulk.Store(time.Now().UnixNano())
	if a.controlIdleEligible() {
		t.Fatal("bulk activity must restore fast polling")
	}
	l.lastBulk.Store(time.Now().Add(-2 * time.Second).UnixNano())
	if !a.controlIdleEligible() {
		t.Fatal("idle backoff must resume after drain")
	}
	l.mobileFeedback.Store(true)
	if a.controlIdleEligible() {
		t.Fatal("mobile")
	}
	l.mobileFeedback.Store(false)
	l.grantChanging = true
	if a.controlIdleEligible() {
		t.Fatal("grant transition")
	}
	l.grantChanging = false
	l.attached = SessionID{}
	if a.controlIdleEligible() {
		t.Fatal("startup")
	}
}
