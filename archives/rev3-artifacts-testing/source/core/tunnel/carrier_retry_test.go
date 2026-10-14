package tunnel

import (
	"testing"
	"time"
)

func TestCarrierRecoveryStartsPromptlyThenPacesDeniedAttempts(t *testing.T) {
	var retry carrierRetryPolicy
	now := time.Now()
	for _, base := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 16 * time.Second} {
		if got := retry.next(now, false); got < base || got > base+base/2 {
			t.Fatal("startup/denied reconnect lost its bounded pacing", got, base)
		}
	}
	if got := retry.next(now, true); got < 0 || got >= 400*time.Millisecond {
		t.Fatal("a working account retained startup backoff", got)
	}
	if got := retry.next(now, false); got < time.Second || got > 1500*time.Millisecond {
		t.Fatal("immediate attempt failure was retried without pacing", got)
	}
	if got := retry.next(now.Add(time.Second), true); got < 2*time.Second || got > 3*time.Second {
		t.Fatal("short authenticated flaps replenished immediate attempts", got)
	}
	if got := retry.next(now.Add(10*time.Second), true); got < 0 || got >= 400*time.Millisecond {
		t.Fatal("later independent outage did not start recovery promptly", got)
	}
}
